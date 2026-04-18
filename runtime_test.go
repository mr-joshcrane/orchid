package orchid

import (
	"testing"
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

	rt.Join(parent.ID, child.ID, "result-data")

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

	text, results := rt.Step(p.ID)

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

	_, results := rt.Step(p.ID)

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

	_, results := rt.Step(p.ID)

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

	_, results := rt.Step(p.ID)

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

	_, results := rt.Step(p.ID)

	// admitted (has capability) but error (no handler)
	if !results[0].Admitted {
		t.Fatal("should be admitted (has capability)")
	}
	if results[0].Error == "" {
		t.Fatal("should error with no handler")
	}
}

func TestRuntime_Step_MissingProcess(t *testing.T) {
	rt := NewRuntime(nopGenerator())
	text, results := rt.Step("nonexistent")

	if text != "" || results != nil {
		t.Fatal("step on missing process should return empty")
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
	rt.Step(p.ID)

	// then: the handler executed exactly once
	if callCount != 1 {
		t.Fatalf("expected bash to execute once, got %d", callCount)
	}
}
