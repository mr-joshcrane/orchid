package orchid

import (
	"strings"
	"testing"
)

func TestBuildContext_LastInput(t *testing.T) {
	log := NewEventLog()
	log.Append(Event{Type: EventProcessSpawned, ProcessID: "p1"})
	log.Append(Event{Type: EventUserInput, ProcessID: "p1", Data: map[string]string{"content": "first"}})
	log.Append(Event{Type: EventAssistantOutput, ProcessID: "p1", Data: map[string]string{"content": "ok"}})
	log.Append(Event{Type: EventUserInput, ProcessID: "p1", Data: map[string]string{"content": "second"}})

	p := &Process{
		ID:   "p1",
		Spec: ProcessSpec{Name: "test"},
		View: LogView{Source: log, StartCut: 0},
	}

	ctx := BuildContext(p)

	if ctx.LastInput != "second" {
		t.Fatalf("LastInput = %q, want %q", ctx.LastInput, "second")
	}
}

func TestBuildContext_Step(t *testing.T) {
	log := NewEventLog()
	log.Append(Event{Type: EventUserInput, ProcessID: "p1", Data: map[string]string{"content": "a"}})
	log.Append(Event{Type: EventAssistantOutput, ProcessID: "p1", Data: map[string]string{"content": "b"}})
	log.Append(Event{Type: EventUserInput, ProcessID: "p1", Data: map[string]string{"content": "c"}})

	p := &Process{
		ID:   "p1",
		View: LogView{Source: log, StartCut: 0},
	}

	ctx := BuildContext(p)

	// 2 UserInput + 1 AssistantOutput = 3 steps
	if ctx.Step != 3 {
		t.Fatalf("Step = %d, want 3", ctx.Step)
	}
}

func TestBuildContext_ToolResults(t *testing.T) {
	log := NewEventLog()
	log.Append(Event{Type: EventEffectObserved, ProcessID: "p1", Data: map[string]string{"tool": "read", "result": "data1"}})
	log.Append(Event{Type: EventEffectObserved, ProcessID: "p1", Data: map[string]string{"tool": "bash", "result": "data2"}})

	p := &Process{
		ID:   "p1",
		View: LogView{Source: log, StartCut: 0},
	}

	ctx := BuildContext(p)

	if len(ctx.ToolResults) != 2 {
		t.Fatalf("ToolResults len = %d, want 2", len(ctx.ToolResults))
	}
	if ctx.ToolResults[0].Tool != "read" || ctx.ToolResults[0].Result != "data1" {
		t.Fatalf("ToolResults[0] = %+v", ctx.ToolResults[0])
	}
	if ctx.ToolResults[1].Tool != "bash" || ctx.ToolResults[1].Result != "data2" {
		t.Fatalf("ToolResults[1] = %+v", ctx.ToolResults[1])
	}
}

func TestBuildContext_ChildResults(t *testing.T) {
	log := NewEventLog()
	log.Append(Event{Type: EventJoin, ProcessID: "p1", Data: map[string]string{"child_id": "c1", "result": "found it"}})

	p := &Process{
		ID:   "p1",
		View: LogView{Source: log, StartCut: 0},
	}

	ctx := BuildContext(p)

	if len(ctx.ChildResults) != 1 {
		t.Fatalf("ChildResults len = %d, want 1", len(ctx.ChildResults))
	}
	if ctx.ChildResults[0].ChildID != "c1" || ctx.ChildResults[0].Result != "found it" {
		t.Fatalf("ChildResults[0] = %+v", ctx.ChildResults[0])
	}
}

func TestBuildContext_Spec(t *testing.T) {
	log := NewEventLog()
	spec := ProcessSpec{Name: "myproc", SystemPrompt: "you are helpful", Model: "gpt-4"}

	p := &Process{
		ID:   "p1",
		Spec: spec,
		View: LogView{Source: log, StartCut: 0},
	}

	ctx := BuildContext(p)
	if ctx.Spec.Name != "myproc" || ctx.Spec.SystemPrompt != "you are helpful" {
		t.Fatalf("Spec not propagated: %+v", ctx.Spec)
	}
}

func TestBuildContext_SeesAllEventsWithoutFilter(t *testing.T) {
	log := NewEventLog()
	log.Append(Event{Type: EventUserInput, ProcessID: "p1", Data: map[string]string{"content": "mine"}})
	log.Append(Event{Type: EventUserInput, ProcessID: "p2", Data: map[string]string{"content": "theirs"}})

	p := &Process{
		ID:   "p1",
		View: LogView{Source: log, StartCut: 0},
	}

	ctx := BuildContext(p)

	if len(ctx.Events) != 2 {
		t.Fatalf("Events len = %d, want 2 (no filter = see everything)", len(ctx.Events))
	}
	if ctx.LastInput != "theirs" {
		t.Fatalf("LastInput = %q, want %q (last input regardless of producer)", ctx.LastInput, "theirs")
	}
}

func TestBuildContext_FilterByProcessID(t *testing.T) {
	log := NewEventLog()
	log.Append(Event{Type: EventUserInput, ProcessID: "p1", Data: map[string]string{"content": "mine"}})
	log.Append(Event{Type: EventUserInput, ProcessID: "p2", Data: map[string]string{"content": "theirs"}})

	p := &Process{
		ID: "p1",
		View: LogView{
			Source:   log,
			StartCut: 0,
			Filter:   func(e Event) bool { return e.ProcessID == "p1" },
		},
	}

	ctx := BuildContext(p)

	if len(ctx.Events) != 1 {
		t.Fatalf("Events len = %d, want 1 (filtered to own events)", len(ctx.Events))
	}
	if ctx.LastInput != "mine" {
		t.Fatalf("LastInput = %q, want %q", ctx.LastInput, "mine")
	}
}

func TestBuildContext_RespectsStartCut(t *testing.T) {
	log := NewEventLog()
	log.Append(Event{Type: EventUserInput, ProcessID: "p1", Data: map[string]string{"content": "before"}}) // ID 0
	log.Append(Event{Type: EventUserInput, ProcessID: "p1", Data: map[string]string{"content": "after"}})  // ID 1

	p := &Process{
		ID:   "p1",
		View: LogView{Source: log, StartCut: 1},
	}

	ctx := BuildContext(p)

	if len(ctx.Events) != 1 {
		t.Fatalf("Events len = %d, want 1 (after StartCut)", len(ctx.Events))
	}
	if ctx.LastInput != "after" {
		t.Fatalf("LastInput = %q, want %q", ctx.LastInput, "after")
	}
}

func TestBuildContext_RespectsViewFilter(t *testing.T) {
	log := NewEventLog()
	log.Append(Event{Type: EventUserInput, ProcessID: "p1", Data: map[string]string{"content": "keep"}})
	log.Append(Event{Type: EventEffectObserved, ProcessID: "p1", Data: map[string]string{"tool": "read", "result": "skip"}})

	p := &Process{
		ID: "p1",
		View: LogView{
			Source:   log,
			StartCut: 0,
			Filter:   func(e Event) bool { return e.Type == EventUserInput },
		},
	}

	ctx := BuildContext(p)

	if len(ctx.Events) != 1 {
		t.Fatalf("Events len = %d, want 1 (filtered)", len(ctx.Events))
	}
	if len(ctx.ToolResults) != 0 {
		t.Fatal("ToolResults should be empty after filter")
	}
}

func TestRenderContext(t *testing.T) {
	ctx := Context{
		Spec: ProcessSpec{SystemPrompt: "you help"},
		Events: []Event{
			{Type: EventUserInput, Data: map[string]string{"content": "hi"}},
			{Type: EventAssistantOutput, Data: map[string]string{"content": "hello"}},
			{Type: EventEffectObserved, Data: map[string]string{"tool": "read", "result": "data"}},
			{Type: EventMessage, Data: map[string]string{"from": "p2", "content": "msg"}},
			{Type: EventJoin, Data: map[string]string{"child_id": "c1", "result": "done"}},
		},
	}

	rendered := RenderContext(ctx)

	if !strings.Contains(rendered, "System: you help") {
		t.Fatal("missing system prompt")
	}
	if !strings.Contains(rendered, "User: hi") {
		t.Fatal("missing user input")
	}
	if !strings.Contains(rendered, "Assistant: hello") {
		t.Fatal("missing assistant output")
	}
	if !strings.Contains(rendered, "Tool [read]: data") {
		t.Fatal("missing tool result")
	}
	if !strings.Contains(rendered, "Message from p2: msg") {
		t.Fatal("missing message")
	}
	if !strings.Contains(rendered, "Child result [c1]: done") {
		t.Fatal("missing child result")
	}
}
