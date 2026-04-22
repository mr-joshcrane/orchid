package orchid

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"
)

func nopGenerator() Generator {
	return GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{Text: "nop"}
	})
}

func TestRuntime_Spawn(t *testing.T) {
	rt := NewRuntime(nopGenerator())

	p := rt.Spawn(ProcessSpec{Name: "worker"}, []Capability{CapRead, CapBash}, "")

	// then: process registered
	if rt.Processes.Get(p.ID) == nil {
		t.Fatal("spawned process not in table")
	}

	// then: capabilities set
	if !p.HasCapability(CapRead) || !p.HasCapability(CapBash) {
		t.Fatal("capabilities not set")
	}
	if p.HasCapability(CapWrite) {
		t.Fatal("should not have write")
	}

	// then: log view starts at current log position (process sees only its own events)
	// The spawn event itself is at index 0, so StartCut was set before the event was appended
	if p.View.Source != rt.Log {
		t.Fatal("view source should be runtime log")
	}

	// then: ProcessSpawned event logged
	events := rt.Log.All()
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

func TestRuntime_Spawn_ParentID(t *testing.T) {
	rt := NewRuntime(nopGenerator())
	p := rt.Spawn(ProcessSpec{Name: "child"}, nil, "parent_1")

	if p.ParentID != "parent_1" {
		t.Fatalf("ParentID = %q, want %q", p.ParentID, "parent_1")
	}
}

func TestRuntime_Fork(t *testing.T) {
	rt := NewRuntime(nopGenerator())
	parent := rt.Spawn(ProcessSpec{Name: "parent"}, nil, "")
	child := rt.Fork(parent.ID, ProcessSpec{Name: "child"}, []Capability{CapRead})

	// then: child exists with parent reference
	if child.ParentID != parent.ID {
		t.Fatalf("child ParentID = %q, want %q", child.ParentID, parent.ID)
	}

	// then: child sees full log (StartCut = 0)
	if child.View.StartCut != 0 {
		t.Fatalf("fork child StartCut = %d, want 0", child.View.StartCut)
	}

	// then: Fork event logged under parent
	events := rt.Log.All()
	found := false
	for _, e := range events {
		if e.Type == EventFork && e.ProcessID == parent.ID && e.Data["child_id"] == child.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("no Fork event")
	}
}

func TestRuntime_Join(t *testing.T) {
	rt := NewRuntime(nopGenerator())
	parent := rt.Spawn(ProcessSpec{Name: "parent"}, nil, "")
	child := rt.Fork(parent.ID, ProcessSpec{Name: "child"}, nil)

	rt.Join(parent.ID, child.ID, "result-data", TokenUsage{})

	// then: Join event logged under parent
	events := rt.Log.All()
	found := false
	for _, e := range events {
		if e.Type == EventJoin && e.ProcessID == parent.ID && e.Data["result"] == "result-data" {
			found = true
		}
	}
	if !found {
		t.Fatal("no Join event with result")
	}

	// then: child marked Done
	if child.State != ProcessDone {
		t.Fatalf("child state = %d, want ProcessDone", child.State)
	}
}

func TestRuntime_Inject(t *testing.T) {
	rt := NewRuntime(nopGenerator())
	p := rt.Spawn(ProcessSpec{Name: "test"}, nil, "")

	rt.Inject(p.ID, "hello world")

	events := rt.Log.All()
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

func TestRuntime_Send(t *testing.T) {
	rt := NewRuntime(nopGenerator())
	p1 := rt.Spawn(ProcessSpec{Name: "sender"}, nil, "")
	p2 := rt.Spawn(ProcessSpec{Name: "receiver"}, nil, "")

	rt.Send(p1.ID, p2.ID, "msg-content")

	events := rt.Log.All()
	found := false
	for _, e := range events {
		if e.Type == EventMessage && e.ProcessID == p2.ID && e.Data["from"] == p1.ID && e.Data["content"] == "msg-content" {
			found = true
		}
	}
	if !found {
		t.Fatal("no Message event")
	}
}

func TestRuntime_Step_NoEffects(t *testing.T) {
	rt := NewRuntime(GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{Text: "thinking"}
	}))
	p := rt.Spawn(ProcessSpec{Name: "thinker"}, nil, "")
	rt.Inject(p.ID, "what?")

	text, results, _ := rt.Step(context.Background(), p.ID)

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

func TestRuntime_Step_EffectAdmittedAndExecuted(t *testing.T) {
	rt := NewRuntime(GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text: "reading",
			Effects: []ProposedEffect{{
				Tool: "read",
				Args: map[string]string{"path": "/tmp/test"},
			}},
		}
	}))

	rt.Effects.RegisterHandler("read", func(args map[string]string) (string, error) {
		return "file-content:" + args["path"], nil
	})

	p := rt.Spawn(ProcessSpec{Name: "reader"}, []Capability{"read"}, "")
	rt.Inject(p.ID, "read it")

	_, results, _ := rt.Step(context.Background(), p.ID)

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

func TestRuntime_Step_EffectRejectedByCapability(t *testing.T) {
	rt := NewRuntime(GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text:    "want to write",
			Effects: []ProposedEffect{{Tool: "write", Args: map[string]string{}}},
		}
	}))

	// given: process only has read capability
	p := rt.Spawn(ProcessSpec{Name: "reader"}, []Capability{CapRead}, "")
	rt.Inject(p.ID, "go")

	_, results, _ := rt.Step(context.Background(), p.ID)

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

func TestRuntime_Step_EffectRejectedByPolicy(t *testing.T) {
	rt := NewRuntime(GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text:    "want to read",
			Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{}}},
		}
	}))

	rt.Effects.RegisterHandler("read", func(args map[string]string) (string, error) {
		return "data", nil
	})
	rt.Effects.AddPolicy(func(p *Process, e *Effect) (bool, string) {
		return false, "policy says no"
	})

	p := rt.Spawn(ProcessSpec{Name: "reader"}, []Capability{"read"}, "")
	rt.Inject(p.ID, "go")

	_, results, _ := rt.Step(context.Background(), p.ID)

	if results[0].Admitted {
		t.Fatal("policy should reject")
	}
	if results[0].Error != "policy says no" {
		t.Fatalf("error = %q", results[0].Error)
	}
}

func TestRuntime_Step_NoHandler(t *testing.T) {
	rt := NewRuntime(GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text:    "call unknown",
			Effects: []ProposedEffect{{Tool: "unknown", Args: map[string]string{}}},
		}
	}))

	p := rt.Spawn(ProcessSpec{Name: "test"}, []Capability{"unknown"}, "")
	rt.Inject(p.ID, "go")

	_, results, _ := rt.Step(context.Background(), p.ID)

	// admitted (has capability) but error (no handler)
	if !results[0].Admitted {
		t.Fatal("should be admitted (has capability)")
	}
	if results[0].Error == "" {
		t.Fatal("should error with no handler")
	}
}

func TestRuntime_Step_EffectEventsMinimal(t *testing.T) {
	rt := NewRuntime(GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text:    "reading",
			Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{"path": "/tmp"}}},
		}
	}))

	rt.Effects.RegisterHandler("read", func(args map[string]string) (string, error) {
		return "data", nil
	})

	p := rt.Spawn(ProcessSpec{Name: "test"}, []Capability{"read"}, "")
	rt.Inject(p.ID, "go")
	rt.Step(context.Background(), p.ID)

	var effectEvents []EventType
	for _, e := range rt.Log.All() {
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

func TestRuntime_Step_MissingProcess(t *testing.T) {
	rt := NewRuntime(nopGenerator())
	text, results, _ := rt.Step(context.Background(), "nonexistent")

	if text != "" || results != nil {
		t.Fatal("step on missing process should return empty")
	}
}

func TestRuntime_Step_ProcessHandlerOverridesGlobal(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text:    "reading",
			Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{"path": "/tmp"}}},
		}
	})

	rt := NewRuntime(gen)
	rt.Effects.RegisterHandler("read", func(args map[string]string) (string, error) {
		return "global", nil
	})

	p := rt.Spawn(ProcessSpec{Name: "test"}, []Capability{"read"}, "")
	p.Handlers = map[string]EffectHandler{
		"read": func(args map[string]string) (string, error) {
			return "local", nil
		},
	}
	rt.Inject(p.ID, "go")

	_, results, _ := rt.Step(context.Background(), p.ID)

	if results[0].Result != "local" {
		t.Fatalf("expected process handler, got %q", results[0].Result)
	}
}

func TestRuntime_Step_FallsBackToGlobalHandler(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text:    "reading",
			Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{"path": "/tmp"}}},
		}
	})

	rt := NewRuntime(gen)
	rt.Effects.RegisterHandler("read", func(args map[string]string) (string, error) {
		return "global", nil
	})

	p := rt.Spawn(ProcessSpec{Name: "test"}, []Capability{"read"}, "")
	rt.Inject(p.ID, "go")

	_, results, _ := rt.Step(context.Background(), p.ID)

	if results[0].Result != "global" {
		t.Fatalf("expected global handler, got %q", results[0].Result)
	}
}

func TestRuntime_Step_MixedHandlerDispatch(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text: "both",
			Effects: []ProposedEffect{
				{Tool: "read", Args: map[string]string{"path": "/a"}},
				{Tool: "bash", Args: map[string]string{"command": "ls"}},
			},
		}
	})

	rt := NewRuntime(gen)
	rt.Effects.RegisterHandler("read", func(args map[string]string) (string, error) {
		return "global-read", nil
	})
	rt.Effects.RegisterHandler("bash", func(args map[string]string) (string, error) {
		return "global-bash", nil
	})

	p := rt.Spawn(ProcessSpec{Name: "test"}, []Capability{"read", "bash"}, "")
	p.Handlers = map[string]EffectHandler{
		"read": func(args map[string]string) (string, error) {
			return "local-read", nil
		},
	}
	rt.Inject(p.ID, "go")

	_, results, _ := rt.Step(context.Background(), p.ID)

	if results[0].Result != "local-read" {
		t.Fatalf("read: expected local handler, got %q", results[0].Result)
	}
	if results[1].Result != "global-bash" {
		t.Fatalf("bash: expected global handler, got %q", results[1].Result)
	}
}

func TestRuntime_Step_SameEffectNotExecutedTwice(t *testing.T) {
	// given: a generator that always proposes the same tool call
	callCount := 0
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text: "",
			Effects: []ProposedEffect{
				{Tool: "bash", Args: map[string]string{"command": "rm -rf /tmp/test"}},
			},
		}
	})

	rt := NewRuntime(gen)
	rt.Effects.RegisterHandler("bash", func(args map[string]string) (string, error) {
		callCount++
		return "", nil
	})

	p := rt.Spawn(ProcessSpec{Name: "agent"}, []Capability{"bash"}, "")
	rt.Inject(p.ID, "run rm -rf /tmp/test")

	// when: we step once (as Run now does)
	rt.Step(context.Background(), p.ID)

	// then: the handler executed exactly once
	if callCount != 1 {
		t.Fatalf("expected bash to execute once, got %d", callCount)
	}
}

func TestRuntime_StepUntilDone_MultiStep(t *testing.T) {
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

	rt := NewRuntime(gen)
	rt.Effects.RegisterHandler("read", func(args map[string]string) (string, error) {
		return "data", nil
	})

	p := rt.Spawn(ProcessSpec{Name: "agent"}, []Capability{"read"}, "")
	rt.Inject(p.ID, "go")

	text, usage, err := rt.StepUntilDone(context.Background(), p.ID, 50, nil)

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

func TestRuntime_StepUntilDone_MaxStepsCap(t *testing.T) {
	stepCount := 0
	gen := GeneratorFunc(func(ctx Context) Proposal {
		stepCount++
		return Proposal{
			Text:    "working",
			Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{"n": fmt.Sprintf("%d", stepCount)}}},
		}
	})

	rt := NewRuntime(gen)
	rt.Effects.RegisterHandler("read", func(args map[string]string) (string, error) {
		return "data", nil
	})

	p := rt.Spawn(ProcessSpec{Name: "agent"}, []Capability{"read"}, "")
	rt.Inject(p.ID, "go")

	rt.StepUntilDone(context.Background(), p.ID, 3, nil)

	if stepCount != 3 {
		t.Fatalf("stepCount = %d, want 3 (capped by maxSteps)", stepCount)
	}
}

func TestRuntime_StepUntilDone_CallbackInjects(t *testing.T) {
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

	rt := NewRuntime(gen)
	rt.Effects.RegisterHandler("read", func(args map[string]string) (string, error) {
		return "data", nil
	})

	p := rt.Spawn(ProcessSpec{Name: "agent"}, []Capability{"read"}, "")
	rt.Inject(p.ID, "go")

	cb := func(step int, text string, results []EffectResult) (string, bool) {
		return "injected message", false
	}

	text, _, _ := rt.StepUntilDone(context.Background(), p.ID, 50, cb)

	if text != "got: injected message" {
		t.Fatalf("text = %q, want callback injection visible", text)
	}
}

func TestRuntime_StepUntilDone_CallbackStops(t *testing.T) {
	stepCount := 0
	gen := GeneratorFunc(func(ctx Context) Proposal {
		stepCount++
		return Proposal{
			Text:    fmt.Sprintf("step-%d", stepCount),
			Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{"n": fmt.Sprintf("%d", stepCount)}}},
		}
	})

	rt := NewRuntime(gen)
	rt.Effects.RegisterHandler("read", func(args map[string]string) (string, error) {
		return "data", nil
	})

	p := rt.Spawn(ProcessSpec{Name: "agent"}, []Capability{"read"}, "")
	rt.Inject(p.ID, "go")

	cb := func(step int, text string, results []EffectResult) (string, bool) {
		if step >= 1 {
			return "stop now", true
		}
		return "", false
	}

	rt.StepUntilDone(context.Background(), p.ID, 50, cb)

	if stepCount != 3 {
		t.Fatalf("stepCount = %d, want 3 (stop after step 1 + final)", stepCount)
	}
}

func TestRuntime_StepUntilDone_NilCallback(t *testing.T) {
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

	rt := NewRuntime(gen)
	rt.Effects.RegisterHandler("read", func(args map[string]string) (string, error) {
		return "data", nil
	})

	p := rt.Spawn(ProcessSpec{Name: "agent"}, []Capability{"read"}, "")
	rt.Inject(p.ID, "go")

	text, _, _ := rt.StepUntilDone(context.Background(), p.ID, 50, nil)

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

	rt := NewRuntime(gen)
	p := rt.Spawn(ProcessSpec{Name: "test", Model: "claude-3"}, nil, "")
	rt.Inject(p.ID, "go")
	rt.Step(context.Background(), p.ID)

	for _, e := range rt.Log.All() {
		if e.Type != EventAssistantOutput {
			continue
		}
		if e.Data["input_tokens"] != "100" {
			t.Fatalf("input_tokens = %q, want %q", e.Data["input_tokens"], "100")
		}
		if e.Data["output_tokens"] != "25" {
			t.Fatalf("output_tokens = %q, want %q", e.Data["output_tokens"], "25")
		}
		if e.Data["model"] != "claude-3" {
			t.Fatalf("model = %q, want %q", e.Data["model"], "claude-3")
		}
		return
	}
	t.Fatal("no AssistantOutput event found")
}

func TestEvent_ProcessSpawned_ContainsModelAndSystem(t *testing.T) {
	rt := NewRuntime(nopGenerator())
	p := rt.Spawn(ProcessSpec{Name: "worker", Model: "claude-3", SystemPrompt: "You are helpful."}, nil, "parent_1")

	for _, e := range rt.Log.All() {
		if e.Type != EventProcessSpawned || e.ProcessID != p.ID {
			continue
		}
		if e.Data["model"] != "claude-3" {
			t.Fatalf("model = %q, want %q", e.Data["model"], "claude-3")
		}
		if e.Data["system_prompt"] != "You are helpful." {
			t.Fatalf("system_prompt = %q", e.Data["system_prompt"])
		}
		return
	}
	t.Fatal("no ProcessSpawned event found")
}

func TestEvent_EffectObserved_ContainsDuration(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text:    "reading",
			Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{}}},
		}
	})

	rt := NewRuntime(gen)
	rt.Effects.RegisterHandler("read", func(args map[string]string) (string, error) {
		time.Sleep(5 * time.Millisecond)
		return "data", nil
	})

	p := rt.Spawn(ProcessSpec{Name: "test"}, []Capability{"read"}, "")
	rt.Inject(p.ID, "go")
	rt.Step(context.Background(), p.ID)

	for _, e := range rt.Log.All() {
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

func TestEvent_Join_ContainsChildTokens(t *testing.T) {
	rt := NewRuntime(nopGenerator())
	parent := rt.Spawn(ProcessSpec{Name: "parent"}, nil, "")
	child := rt.Fork(parent.ID, ProcessSpec{Name: "child"}, nil)

	rt.Join(parent.ID, child.ID, "result", TokenUsage{InputTokens: 500, OutputTokens: 100})

	for _, e := range rt.Log.All() {
		if e.Type != EventJoin {
			continue
		}
		if e.Data["input_tokens"] != "500" {
			t.Fatalf("input_tokens = %q, want %q", e.Data["input_tokens"], "500")
		}
		if e.Data["output_tokens"] != "100" {
			t.Fatalf("output_tokens = %q, want %q", e.Data["output_tokens"], "100")
		}
		return
	}
	t.Fatal("no Join event found")
}

func TestProcess_Usage_AccumulatesAcrossSteps(t *testing.T) {
	stepCount := 0
	gen := GeneratorFunc(func(ctx Context) Proposal {
		stepCount++
		return Proposal{Text: "ok", InputTokens: 100, OutputTokens: 20}
	})

	rt := NewRuntime(gen)
	p := rt.Spawn(ProcessSpec{Name: "test"}, nil, "")
	rt.Inject(p.ID, "go")

	rt.Step(context.Background(), p.ID)
	rt.Step(context.Background(), p.ID)
	rt.Step(context.Background(), p.ID)

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

	rt := NewRuntime(gen)
	p1 := rt.Spawn(ProcessSpec{Name: "a"}, nil, "")
	p2 := rt.Spawn(ProcessSpec{Name: "b"}, nil, "")

	rt.Inject(p1.ID, "go")
	rt.Inject(p2.ID, "go")

	rt.Step(context.Background(), p1.ID)
	rt.Step(context.Background(), p1.ID)
	rt.Step(context.Background(), p2.ID)

	if p1.Usage.InputTokens != 100 {
		t.Fatalf("p1.InputTokens = %d, want 100", p1.Usage.InputTokens)
	}
	if p2.Usage.InputTokens != 50 {
		t.Fatalf("p2.InputTokens = %d, want 50", p2.Usage.InputTokens)
	}
}
