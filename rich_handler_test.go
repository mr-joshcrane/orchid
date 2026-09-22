package orchid

import (
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
	_, results, _, _ := wf.Step(context.Background(), p.ID)

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
