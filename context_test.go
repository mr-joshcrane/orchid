package orchid

import (
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
		Name: "test",
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

func TestBuildContext_NameAndPurpose(t *testing.T) {
	log := NewEventLog()

	p := &Process{
		ID:      "p1",
		Name:    "myproc",
		Purpose: "you are helpful",
		View:    LogView{Source: log, StartCut: 0},
	}

	ctx := BuildContext(p)
	if ctx.Name != "myproc" || ctx.Purpose != "you are helpful" {
		t.Fatalf("Name/Purpose not propagated: Name=%q Purpose=%q", ctx.Name, ctx.Purpose)
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
	log.Append(Event{Type: EventUserInput, ProcessID: "p1", Data: map[string]string{"content": "before"}})
	log.Append(Event{Type: EventUserInput, ProcessID: "p1", Data: map[string]string{"content": "after"}})

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
