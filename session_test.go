package orchid

import (
	"bytes"
	"context"
	"testing"
)

func TestRichHandlerRegistration(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		if len(ctx.ToolResults) > 0 {
			return Proposal{Text: "done"}
		}
		return Proposal{
			Text:    "reading",
			Effects: []ProposedEffect{{Tool: "rich", Args: map[string]string{}}},
		}
	})

	richTool := Tool{
		Name: "rich",
		RichHandler: func(hctx HandlerContext, args map[string]string) (Result, error) {
			return Result{
				Content:  "file-data",
				MIME:     "text/plain",
				Bytes:    9,
				Metadata: map[string]string{"source": "disk"},
			}, nil
		},
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(richTool))
	if err != nil {
		t.Fatal(err)
	}

	p := wf.Spawn("test", "", []Capability{"rich"}, "")
	p.Tools = []ToolDefinition{{Name: "rich"}}
	p.View.Filter = func(e Event) bool { return e.ProcessID == p.ID }
	wf.Inject(p.ID, "go")
	_, results, _ := wf.Step(context.Background(), p.ID)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Result != "file-data" {
		t.Fatalf("result = %q, want %q", results[0].Result, "file-data")
	}
}

func TestRichHandlerRejectsMultipleHandlers(t *testing.T) {
	et := NewEffectTable()
	tool := Tool{
		Name:    "bad",
		Handler: func(args map[string]string) (string, error) { return "", nil },
		RichHandler: func(hctx HandlerContext, args map[string]string) (Result, error) {
			return Result{}, nil
		},
	}
	if err := et.RegisterTool(tool); err == nil {
		t.Fatal("expected error for tool with both Handler and RichHandler")
	}
}

func TestRichHandlerWithMiddleware(t *testing.T) {
	var mwSaw string
	mw := func(next ContextualEffectHandler) ContextualEffectHandler {
		return func(hctx HandlerContext, args map[string]string) (string, error) {
			result, err := next(hctx, args)
			mwSaw = result
			return result, err
		}
	}

	richTool := Tool{
		Name: "rich",
		RichHandler: func(hctx HandlerContext, args map[string]string) (Result, error) {
			return Result{Content: "rich-content"}, nil
		},
	}
	wrapped := richTool.With(mw)
	if wrapped.RichHandler != nil {
		t.Fatal("With() should nil RichHandler and move to ContextualHandler")
	}
	if wrapped.ContextualHandler == nil {
		t.Fatal("With() should set ContextualHandler")
	}

	result, err := wrapped.ContextualHandler(HandlerContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != "rich-content" {
		t.Fatalf("result = %q", result)
	}
	if mwSaw != "rich-content" {
		t.Fatalf("middleware saw %q", mwSaw)
	}
}

// resumeGen records the derived history it is handed, so a test can assert what
// the model would see. Its first step proposes one tool call.
type resumeGen struct {
	step int
	seen [][]Message
}

func (g *resumeGen) Generate(ctx Context) Proposal {
	g.seen = append(g.seen, ctx.Messages)
	g.step++
	if g.step == 1 {
		return Proposal{
			Text:    "working",
			Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{"n": "1"}, ToolUseID: "call_1"}},
		}
	}
	return Proposal{Text: "done"}
}

func TestSaveAndResumeSession(t *testing.T) {
	readTool := Tool{
		Name:    "read",
		Handler: func(args map[string]string) (string, error) { return "data", nil },
	}

	gen1 := &resumeGen{}
	wf1, err := NewWorkflowWithOptions(gen1, WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}

	p := wf1.Spawn("agent", "test agent", []Capability{"read"}, "")
	p.Tools = []ToolDefinition{{Name: "read"}}
	p.View.Filter = func(e Event) bool { return e.ProcessID == p.ID }
	wf1.Inject(p.ID, "go")
	wf1.Step(context.Background(), p.ID)

	var buf bytes.Buffer
	if err := wf1.SaveSession(&buf); err != nil {
		t.Fatal(err)
	}

	savedBytes := buf.Bytes()
	if len(savedBytes) == 0 {
		t.Fatal("saved session should not be empty")
	}

	gen2 := &resumeGen{step: 1}
	wf2, err := NewWorkflowWithOptions(gen2, WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}

	if err := wf2.LoadSession(bytes.NewReader(savedBytes)); err != nil {
		t.Fatal(err)
	}

	loaded := wf2.Processes.All()
	if len(loaded) == 0 {
		t.Fatal("no processes loaded")
	}

	lp := loaded[0]
	if lp.Name != "agent" {
		t.Fatalf("loaded process name = %q, want %q", lp.Name, "agent")
	}

	lp.View.Filter = func(e Event) bool { return e.ProcessID == lp.ID }
	text, _, _ := wf2.Step(context.Background(), lp.ID)
	if text != "done" {
		t.Fatalf("resumed run text = %q, want %q", text, "done")
	}

	if len(gen2.seen) != 1 {
		t.Fatalf("resumed generator ran %d times, want 1", len(gen2.seen))
	}
	assertToolCallsAnswered(t, gen2.seen[0])
}

// assertToolCallsAnswered fails unless every tool call in the history has a
// matching result and no result is orphaned. Providers reject either.
func assertToolCallsAnswered(t *testing.T, messages []Message) {
	t.Helper()
	answered := make(map[string]bool)
	for _, m := range messages {
		for _, b := range m.Content {
			if b.ToolUse != nil {
				answered[b.ToolUse.ToolUseID] = false
			}
			if b.ToolResult == nil {
				continue
			}
			if _, ok := answered[b.ToolResult.ToolUseID]; !ok {
				t.Fatalf("tool result %q answers no tool call", b.ToolResult.ToolUseID)
			}
			answered[b.ToolResult.ToolUseID] = true
		}
	}
	if len(answered) == 0 {
		t.Fatal("resumed history contains no tool call; the dispatched call was lost")
	}
	for id, ok := range answered {
		if !ok {
			t.Fatalf("tool call %q has no matching result", id)
		}
	}
}
