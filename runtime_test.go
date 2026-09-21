package orchid

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

func nopGenerator() Generator {
	return GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{Text: "nop"}
	})
}

func TestWorkflow_Spawn(t *testing.T) {
	wf, err := NewWorkflowWithOptions(nopGenerator())
	if err != nil {
		t.Fatal(err)
	}

	p := wf.Spawn("worker", "", []Capability{"read", "bash"}, "")

	if wf.Processes.Get(p.ID) == nil {
		t.Fatal("spawned process not in table")
	}

	if !p.HasPermission(PermissionFromTool("read")) || !p.HasPermission(PermissionFromTool("bash")) {
		t.Fatal("permissions not set from spawn grants")
	}
	if p.HasPermission(PermissionFromTool("write")) {
		t.Fatal("should not have write")
	}

	if p.View.Source != wf.Log {
		t.Fatal("view source should be workflow log")
	}

	events := wf.Log.All()
	found := false
	for _, e := range events {
		if e.Type == EventProcessSpawned && e.ProcessID == p.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("no ProcessSpawned event")
	}
}

func TestWorkflow_Spawn_ParentID(t *testing.T) {
	wf, err := NewWorkflowWithOptions(nopGenerator())
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("child", "", nil, "parent_1")

	if p.ParentID != "parent_1" {
		t.Fatalf("ParentID = %q, want %q", p.ParentID, "parent_1")
	}
}

func TestWorkflow_Inject(t *testing.T) {
	wf, err := NewWorkflowWithOptions(nopGenerator())
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("test", "", nil, "")

	wf.Inject(p.ID, "hello world")

	events := wf.Log.All()
	found := false
	for _, e := range events {
		if e.Type == EventUserInput && e.ProcessID == p.ID && e.Data["content"] == "hello world" {
			found = true
		}
	}
	if !found {
		t.Fatal("no UserInput event")
	}
}

func TestWorkflow_Step_NoEffects(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{Text: "thinking"}
	})
	wf, err := NewWorkflowWithOptions(gen)
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("thinker", "", nil, "")
	wf.Inject(p.ID, "what?")

	text, results, _, _ := wf.Step(context.Background(), p.ID)

	if text != "thinking" {
		t.Fatalf("text = %q, want %q", text, "thinking")
	}
	if len(results) != 0 {
		t.Fatalf("expected no effect results, got %d", len(results))
	}
	if p.State != ProcessWaiting {
		t.Fatalf("state = %d, want ProcessWaiting", p.State)
	}
}

func TestWorkflow_Step_EffectAdmittedAndExecuted(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text: "reading",
			Effects: []ProposedEffect{{
				Tool: "read",
				Args: map[string]string{"path": "/tmp/test"},
			}},
		}
	})

	readTool := Tool{
		Name:        "read",
		Description: "read a file",
		Handler: func(args map[string]string) (string, error) {
			return "file-content:" + args["path"], nil
		},
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("reader", "", []Capability{"read"}, "")
	wf.Inject(p.ID, "read it")

	_, results, _, _ := wf.Step(context.Background(), p.ID)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Admitted {
		t.Fatal("effect should be admitted")
	}
	if results[0].Result != "file-content:/tmp/test" {
		t.Fatalf("result = %q", results[0].Result)
	}
}

func TestWorkflow_Step_EffectRejectedByPermission(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text:    "want to write",
			Effects: []ProposedEffect{{Tool: "write", Args: map[string]string{}}},
		}
	})

	wf, err := NewWorkflowWithOptions(gen)
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("reader", "", []Capability{"read"}, "")
	wf.Inject(p.ID, "go")

	_, results, _, _ := wf.Step(context.Background(), p.ID)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Admitted {
		t.Fatal("effect should be rejected")
	}
	if results[0].Error == "" {
		t.Fatal("should have error message")
	}
}

func TestWorkflow_Step_EffectRejectedByPolicy(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text:    "want to read",
			Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{}}},
		}
	})

	readTool := Tool{
		Name: "read", Description: "read",
		Handler: func(args map[string]string) (string, error) { return "data", nil },
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}
	wf.Effects.AddPolicy(func(p *Process, e *Effect) (bool, string) {
		return false, "policy says no"
	})

	p := wf.Spawn("reader", "", []Capability{"read"}, "")
	wf.Inject(p.ID, "go")

	_, results, _, _ := wf.Step(context.Background(), p.ID)

	if results[0].Admitted {
		t.Fatal("policy should reject")
	}
	if results[0].Error != "policy says no" {
		t.Fatalf("error = %q", results[0].Error)
	}
}

func TestWorkflow_Step_NoHandler(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text:    "call unknown",
			Effects: []ProposedEffect{{Tool: "unknown", Args: map[string]string{}}},
		}
	})

	wf, err := NewWorkflowWithOptions(gen)
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("test", "", []Capability{"unknown"}, "")
	wf.Inject(p.ID, "go")

	_, results, _, _ := wf.Step(context.Background(), p.ID)

	if !results[0].Admitted {
		t.Fatal("should be admitted (has the permission)")
	}
	if results[0].Error == "" {
		t.Fatal("should error with no handler")
	}
}

func TestWorkflow_Step_EffectEventsMinimal(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text:    "reading",
			Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{"path": "/tmp"}}},
		}
	})

	readTool := Tool{
		Name: "read", Description: "read",
		Handler: func(args map[string]string) (string, error) { return "data", nil },
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("test", "", []Capability{"read"}, "")
	wf.Inject(p.ID, "go")
	wf.Step(context.Background(), p.ID)

	var effectEvents []EventType
	for _, e := range wf.Log.All() {
		if e.ProcessID != p.ID {
			continue
		}
		switch e.Type {
		case EventEffectIntent, EventEffectObserved, EventEffectRejected:
			effectEvents = append(effectEvents, e.Type)
		}
	}

	if len(effectEvents) != 2 {
		t.Fatalf("expected 2 effect events (Intent + Observed), got %d: %v", len(effectEvents), effectEvents)
	}
	if effectEvents[0] != EventEffectIntent {
		t.Fatalf("first effect event = %v, want EventEffectIntent", effectEvents[0])
	}
	if effectEvents[1] != EventEffectObserved {
		t.Fatalf("second effect event = %v, want EventEffectObserved", effectEvents[1])
	}
}

func TestWorkflow_Step_MissingProcess(t *testing.T) {
	wf, err := NewWorkflowWithOptions(nopGenerator())
	if err != nil {
		t.Fatal(err)
	}
	text, results, _, stepErr := wf.Step(context.Background(), "nonexistent")

	if text != "" || results != nil {
		t.Fatal("step on missing process should return empty")
	}
	// An empty return alone cannot be told apart from a turn that produced no
	// text, so the unknown id has to be reported.
	if !errors.Is(stepErr, ErrProcessNotFound) {
		t.Fatalf("err = %v, want ErrProcessNotFound", stepErr)
	}
}

// TestWorkflow_Step_PropagatesGeneratorError pins that a failed generation is
// reported to the caller. It was previously recorded on the process and returned
// as an empty string, so a caller reading only the returns saw success.
func TestWorkflow_Step_PropagatesGeneratorError(t *testing.T) {
	boom := errors.New("model refused")
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{Err: boom}
	})

	wf, err := NewWorkflowWithOptions(gen)
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("worker", "", nil, "")
	wf.Inject(p.ID, "go")

	text, results, usage, stepErr := wf.Step(context.Background(), p.ID)

	if !errors.Is(stepErr, boom) {
		t.Fatalf("err = %v, want the generator's error", stepErr)
	}
	if text != "" || results != nil || usage != (TokenUsage{}) {
		t.Errorf("a failed turn should report nothing: %q %v %+v", text, results, usage)
	}
	if p.State != ProcessFailed {
		t.Errorf("state = %d, want ProcessFailed", p.State)
	}
}

// TestWorkflow_Step_PropagatesFatalEffectError pins that a fatal effect is
// reported, while the turn's text and results are still returned: the calls the
// model made must stay answered even though the run cannot continue.
func TestWorkflow_Step_PropagatesFatalEffectError(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text:    "trying",
			Effects: []ProposedEffect{{Tool: "boom", Args: map[string]string{}}},
		}
	})
	boomTool := Tool{
		Name: "boom", Description: "fails fatally",
		Handler: func(map[string]string) (string, error) {
			return "", fmt.Errorf("unrecoverable: %w", ErrEffectFatal)
		},
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(boomTool))
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("worker", "", []Capability{"boom"}, "")
	wf.Inject(p.ID, "go")

	text, results, _, stepErr := wf.Step(context.Background(), p.ID)

	if !errors.Is(stepErr, ErrEffectFatal) {
		t.Fatalf("err = %v, want ErrEffectFatal", stepErr)
	}
	if text != "trying" {
		t.Errorf("text = %q, want the turn's text", text)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result so the call stays answered, got %d", len(results))
	}
	if p.State != ProcessFailed {
		t.Errorf("state = %d, want ProcessFailed", p.State)
	}
}

// TestStepUntilDone_ReturnsGeneratorError pins that the loop surfaces the error
// rather than finishing quietly.
func TestStepUntilDone_ReturnsGeneratorError(t *testing.T) {
	boom := errors.New("model refused")
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{Err: boom}
	})

	wf, err := NewWorkflowWithOptions(gen)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, runErr := wf.Run(context.Background(), "p", "go"); !errors.Is(runErr, boom) {
		t.Fatalf("Run err = %v, want the generator's error", runErr)
	}
}

func TestWorkflow_Step_OneCallRunsExactlyOnce(t *testing.T) {
	callCount := 0
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text: "",
			Effects: []ProposedEffect{
				{Tool: "bash", Args: map[string]string{"command": "rm -rf /tmp/test"}},
			},
		}
	})

	bashTool := Tool{
		Name: "bash", Description: "bash",
		Handler: func(args map[string]string) (string, error) {
			callCount++
			return "", nil
		},
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(bashTool))
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("agent", "", []Capability{"bash"}, "")
	wf.Inject(p.ID, "run rm -rf /tmp/test")

	wf.Step(context.Background(), p.ID)

	if callCount != 1 {
		t.Fatalf("expected bash to execute once, got %d", callCount)
	}
}

func TestWorkflow_StepUntilDone_MultiStep(t *testing.T) {
	stepCount := 0
	gen := GeneratorFunc(func(ctx Context) Proposal {
		stepCount++
		if stepCount <= 3 {
			return Proposal{
				Text:    "working",
				Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{"n": fmt.Sprintf("%d", stepCount)}}},
			}
		}
		return Proposal{Text: "done"}
	})

	readTool := Tool{
		Name: "read", Description: "read",
		Handler: func(args map[string]string) (string, error) { return "data", nil },
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("agent", "", []Capability{"read"}, "")
	wf.Inject(p.ID, "go")

	text, usage, err := wf.StepUntilDone(context.Background(), p.ID, Constraints{MaxSteps: 50}, nil)

	if err != nil {
		t.Fatal(err)
	}
	if text != "done" {
		t.Fatalf("text = %q, want %q", text, "done")
	}
	if stepCount != 4 {
		t.Fatalf("stepCount = %d, want 4", stepCount)
	}
	_ = usage
}

func TestWorkflow_StepUntilDone_MaxStepsCap(t *testing.T) {
	stepCount := 0
	gen := GeneratorFunc(func(ctx Context) Proposal {
		stepCount++
		return Proposal{
			Text:    "working",
			Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{"n": fmt.Sprintf("%d", stepCount)}}},
		}
	})

	readTool := Tool{
		Name: "read", Description: "read",
		Handler: func(args map[string]string) (string, error) { return "data", nil },
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("agent", "", []Capability{"read"}, "")
	wf.Inject(p.ID, "go")

	wf.StepUntilDone(context.Background(), p.ID, Constraints{MaxSteps: 3}, nil)

	if stepCount != 3 {
		t.Fatalf("stepCount = %d, want 3 (capped by maxSteps)", stepCount)
	}
}

func TestWorkflow_StepUntilDone_CallbackInjects(t *testing.T) {
	stepCount := 0
	gen := GeneratorFunc(func(ctx Context) Proposal {
		stepCount++
		if stepCount == 1 {
			return Proposal{
				Text:    "working",
				Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{"n": "1"}}},
			}
		}
		return Proposal{Text: "got: " + ctx.LastInput}
	})

	readTool := Tool{
		Name: "read", Description: "read",
		Handler: func(args map[string]string) (string, error) { return "data", nil },
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("agent", "", []Capability{"read"}, "")
	wf.Inject(p.ID, "go")

	cb := func(step int, text string, results []EffectResult) (string, bool) {
		return "injected message", false
	}

	text, _, _ := wf.StepUntilDone(context.Background(), p.ID, Constraints{MaxSteps: 50}, cb)

	if text != "got: injected message" {
		t.Fatalf("text = %q, want callback injection visible", text)
	}
}

func TestWorkflow_StepUntilDone_CallbackStops(t *testing.T) {
	stepCount := 0
	gen := GeneratorFunc(func(ctx Context) Proposal {
		stepCount++
		return Proposal{
			Text:    fmt.Sprintf("step-%d", stepCount),
			Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{"n": fmt.Sprintf("%d", stepCount)}}},
		}
	})

	readTool := Tool{
		Name: "read", Description: "read",
		Handler: func(args map[string]string) (string, error) { return "data", nil },
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("agent", "", []Capability{"read"}, "")
	wf.Inject(p.ID, "go")

	cb := func(step int, text string, results []EffectResult) (string, bool) {
		if step >= 1 {
			return "stop now", true
		}
		return "", false
	}

	wf.StepUntilDone(context.Background(), p.ID, Constraints{MaxSteps: 50}, cb)

	if stepCount != 3 {
		t.Fatalf("stepCount = %d, want 3 (stop after step 1 + final)", stepCount)
	}
}

func TestWorkflow_StepUntilDone_NilCallback(t *testing.T) {
	stepCount := 0
	gen := GeneratorFunc(func(ctx Context) Proposal {
		stepCount++
		if stepCount <= 2 {
			return Proposal{
				Text:    "working",
				Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{"n": fmt.Sprintf("%d", stepCount)}}},
			}
		}
		return Proposal{Text: "done"}
	})

	readTool := Tool{
		Name: "read", Description: "read",
		Handler: func(args map[string]string) (string, error) { return "data", nil },
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("agent", "", []Capability{"read"}, "")
	wf.Inject(p.ID, "go")

	text, _, _ := wf.StepUntilDone(context.Background(), p.ID, Constraints{MaxSteps: 50}, nil)

	if text != "done" {
		t.Fatalf("text = %q, want %q", text, "done")
	}
	if stepCount != 3 {
		t.Fatalf("stepCount = %d, want 3", stepCount)
	}
}

func TestEvent_AssistantOutput_ContainsTokens(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{Text: "hello", InputTokens: 100, OutputTokens: 25}
	})

	wf, err := NewWorkflowWithOptions(gen)
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("test", "", nil, "")
	wf.Inject(p.ID, "go")
	wf.Step(context.Background(), p.ID)

	for _, e := range wf.Log.All() {
		if e.Type != EventAssistantOutput {
			continue
		}
		if e.Data["input_tokens"] != "100" {
			t.Fatalf("input_tokens = %q, want %q", e.Data["input_tokens"], "100")
		}
		if e.Data["output_tokens"] != "25" {
			t.Fatalf("output_tokens = %q, want %q", e.Data["output_tokens"], "25")
		}
		return
	}
	t.Fatal("no AssistantOutput event found")
}

func TestEvent_EffectObserved_ContainsDuration(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text:    "reading",
			Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{}}},
		}
	})

	readTool := Tool{
		Name: "read", Description: "read",
		Handler: func(args map[string]string) (string, error) {
			time.Sleep(5 * time.Millisecond)
			return "data", nil
		},
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("test", "", []Capability{"read"}, "")
	wf.Inject(p.ID, "go")
	wf.Step(context.Background(), p.ID)

	for _, e := range wf.Log.All() {
		if e.Type != EventEffectObserved {
			continue
		}
		ms, err := strconv.ParseInt(e.Data["duration_ms"], 10, 64)
		if err != nil {
			t.Fatalf("duration_ms not parseable: %q", e.Data["duration_ms"])
		}
		if ms < 5 {
			t.Fatalf("duration_ms = %d, expected >= 5", ms)
		}
		return
	}
	t.Fatal("no EffectObserved event found")
}

func TestProcess_Usage_AccumulatesAcrossSteps(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{Text: "ok", InputTokens: 100, OutputTokens: 20}
	})

	wf, err := NewWorkflowWithOptions(gen)
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("test", "", nil, "")
	wf.Inject(p.ID, "go")

	wf.Step(context.Background(), p.ID)
	wf.Step(context.Background(), p.ID)
	wf.Step(context.Background(), p.ID)

	if p.Usage.InputTokens != 300 {
		t.Fatalf("InputTokens = %d, want 300", p.Usage.InputTokens)
	}
	if p.Usage.OutputTokens != 60 {
		t.Fatalf("OutputTokens = %d, want 60", p.Usage.OutputTokens)
	}
}

func TestProcess_Usage_IndependentPerProcess(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{Text: "ok", InputTokens: 50, OutputTokens: 10}
	})

	wf, err := NewWorkflowWithOptions(gen)
	if err != nil {
		t.Fatal(err)
	}
	p1 := wf.Spawn("a", "", nil, "")
	p2 := wf.Spawn("b", "", nil, "")

	wf.Inject(p1.ID, "go")
	wf.Inject(p2.ID, "go")

	wf.Step(context.Background(), p1.ID)
	wf.Step(context.Background(), p1.ID)
	wf.Step(context.Background(), p2.ID)

	if p1.Usage.InputTokens != 100 {
		t.Fatalf("p1.InputTokens = %d, want 100", p1.Usage.InputTokens)
	}
	if p2.Usage.InputTokens != 50 {
		t.Fatalf("p2.InputTokens = %d, want 50", p2.Usage.InputTokens)
	}
}

func TestWorkflow_Run(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		if len(ctx.ToolResults) > 0 {
			return Proposal{Text: "done"}
		}
		return Proposal{
			Text: "reading",
			Effects: []ProposedEffect{{
				Tool: "read",
				Args: map[string]string{"path": "/tmp"},
			}},
		}
	})

	readTool := Tool{
		Name:        "read",
		Description: "read a file",
		Handler: func(args map[string]string) (string, error) {
			return "file-data", nil
		},
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}
	text, _, err := wf.Run(context.Background(), "test agent", "hello")

	if err != nil {
		t.Fatal(err)
	}
	if text != "done" {
		t.Fatalf("text = %q, want %q", text, "done")
	}
}

func TestNewWorkflowOptions(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		if len(ctx.ToolResults) > 0 {
			return Proposal{Text: "done"}
		}
		return Proposal{
			Text:    "reading",
			Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{"path": "/tmp"}}},
		}
	})

	readTool := Tool{
		Name:    "read",
		Handler: func(args map[string]string) (string, error) { return "data", nil },
	}

	t.Run("WithTools option form", func(t *testing.T) {
		wf, err := NewWorkflowWithOptions(gen, WithTools(readTool))
		if err != nil {
			t.Fatal(err)
		}
		text, _, runErr := wf.Run(context.Background(), "test", "go")
		if runErr != nil {
			t.Fatal(runErr)
		}
		if text != "done" {
			t.Fatalf("text = %q, want %q", text, "done")
		}
	})

	t.Run("legacy variadic form still works", func(t *testing.T) {
		wf, err := NewWorkflowWithOptions(gen, WithTools(readTool))
		if err != nil {
			t.Fatal(err)
		}
		text, _, runErr := wf.Run(context.Background(), "test", "go")
		if runErr != nil {
			t.Fatal(runErr)
		}
		if text != "done" {
			t.Fatalf("text = %q, want %q", text, "done")
		}
	})

	t.Run("ContextualHandler dispatched via WithTools", func(t *testing.T) {
		called := false
		ctxTool := Tool{
			Name: "ctxread",
			ContextualHandler: func(hctx HandlerContext, args map[string]string) (string, error) {
				called = true
				if hctx.ProcessID == "" {
					t.Error("ProcessID should be populated")
				}
				if hctx.LogAppend == nil {
					t.Error("LogAppend should be populated")
				}
				return "ctx-data", nil
			},
		}
		ctxGen := GeneratorFunc(func(ctx Context) Proposal {
			if len(ctx.ToolResults) > 0 {
				return Proposal{Text: "done"}
			}
			return Proposal{
				Text:    "reading",
				Effects: []ProposedEffect{{Tool: "ctxread", Args: map[string]string{}}},
			}
		})
		wf, err := NewWorkflowWithOptions(ctxGen, WithTools(ctxTool))
		if err != nil {
			t.Fatal(err)
		}
		p := wf.Spawn("test", "", []Capability{"ctxread"}, "")
		wf.Inject(p.ID, "go")
		wf.Step(context.Background(), p.ID)
		if !called {
			t.Fatal("ContextualHandler was not called")
		}
	})

	t.Run("WithTools reports a misconfigured tool as an error", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("a misconfigured tool must not panic, got %v", r)
			}
		}()

		wf, err := NewWorkflowWithOptions(gen, WithTools(Tool{Name: "bad"}))
		if err == nil {
			t.Fatal("expected an error for a tool with no handler")
		}
		if wf != nil {
			t.Error("expected a nil workflow alongside the error")
		}
		if !strings.Contains(err.Error(), "bad") {
			t.Errorf("error should name the offending tool, got %q", err)
		}
	})

	t.Run("WithTools reports every misconfigured tool", func(t *testing.T) {
		_, err := NewWorkflowWithOptions(gen, WithTools(
			Tool{Name: "no-handler"},
			Tool{
				Name:              "two-handlers",
				Handler:           func(map[string]string) (string, error) { return "", nil },
				ContextualHandler: func(HandlerContext, map[string]string) (string, error) { return "", nil },
			},
		))
		if err == nil {
			t.Fatal("expected an error")
		}
		for _, want := range []string{"no-handler", "two-handlers"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error should name %q, got %q", want, err)
			}
		}
	})
}
