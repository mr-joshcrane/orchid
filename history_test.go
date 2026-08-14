package orchid

import (
	"context"
	"testing"
)

func TestDeriveMessagesBasicTurn(t *testing.T) {
	events := []Event{
		{Type: EventUserInput, Data: map[string]string{KeyContent: "go"}},
		{Type: EventAssistantOutput, Data: map[string]string{KeyContent: "reading"}},
		{Type: EventEffectIntent, Data: map[string]string{
			KeyTool: "read", KeyToolUseID: "c1", ArgPrefix + "path": "/tmp/x",
		}},
		{Type: EventEffectObserved, Data: map[string]string{KeyToolUseID: "c1", KeyResult: "data"}},
		{Type: EventAssistantOutput, Data: map[string]string{KeyContent: "done"}},
	}

	msgs := DeriveMessages(events)

	if len(msgs) != 4 {
		t.Fatalf("derived %d messages, want 4: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != RoleUser || messageText(msgs[0]) != "go" {
		t.Fatalf("message 0 = %+v, want user \"go\"", msgs[0])
	}
	if msgs[1].Role != RoleAssistant || messageText(msgs[1]) != "reading" {
		t.Fatalf("message 1 = %+v, want assistant \"reading\"", msgs[1])
	}
	call := findToolUse(msgs[1])
	if call == nil {
		t.Fatal("assistant message carries no tool call")
	}
	if call.Name != "read" || call.ToolUseID != "c1" {
		t.Fatalf("tool call = %+v, want read/c1", call)
	}
	if call.Input["path"] != "/tmp/x" {
		t.Fatalf("tool call input = %+v, want path=/tmp/x", call.Input)
	}
	if msgs[2].Role != RoleUser {
		t.Fatalf("message 2 role = %q, want user", msgs[2].Role)
	}
	result := msgs[2].Content[0].ToolResult
	if result.ToolUseID != "c1" || toolResultText(result) != "data" {
		t.Fatalf("tool result = %+v, want c1/data", result)
	}
	if result.Status != StatusSuccess {
		t.Fatalf("tool result status = %q, want %q", result.Status, StatusSuccess)
	}
	if messageText(msgs[3]) != "done" {
		t.Fatalf("message 3 = %+v, want assistant \"done\"", msgs[3])
	}
}

// Results are grouped in the order the model asked for them, not the order the
// handlers happened to finish in.
func TestDeriveMessagesOrdersResultsByCallOrder(t *testing.T) {
	events := []Event{
		{Type: EventAssistantOutput, Data: map[string]string{KeyContent: "two reads"}},
		{Type: EventEffectIntent, Data: map[string]string{KeyTool: "read", KeyToolUseID: "first"}},
		{Type: EventEffectIntent, Data: map[string]string{KeyTool: "read", KeyToolUseID: "second"}},
		{Type: EventEffectObserved, Data: map[string]string{KeyToolUseID: "second", KeyResult: "b"}},
		{Type: EventEffectObserved, Data: map[string]string{KeyToolUseID: "first", KeyResult: "a"}},
	}

	msgs := DeriveMessages(events)

	if len(msgs) != 2 {
		t.Fatalf("derived %d messages, want 2", len(msgs))
	}
	results := msgs[1].Content
	if len(results) != 2 {
		t.Fatalf("derived %d results, want 2", len(results))
	}
	if results[0].ToolResult.ToolUseID != "first" || results[1].ToolResult.ToolUseID != "second" {
		t.Fatalf("result order = %q, %q; want first, second",
			results[0].ToolResult.ToolUseID, results[1].ToolResult.ToolUseID)
	}
}

func TestDeriveMessagesDropsUnpairedResult(t *testing.T) {
	events := []Event{
		{Type: EventAssistantOutput, Data: map[string]string{KeyContent: "hi"}},
		{Type: EventEffectObserved, Data: map[string]string{KeyToolUseID: "ghost", KeyResult: "data"}},
	}

	msgs := DeriveMessages(events)

	if len(msgs) != 1 {
		t.Fatalf("derived %d messages, want 1 (the orphan result must be dropped): %+v", len(msgs), msgs)
	}
}

// A tool argument named like an event metadata key must not overwrite it.
func TestDeriveMessagesArgumentsCannotShadowMetadata(t *testing.T) {
	events := []Event{
		{Type: EventAssistantOutput, Data: map[string]string{KeyContent: "call"}},
		{Type: EventEffectIntent, Data: map[string]string{
			KeyTool:                  "read",
			KeyEffectID:              "eff_1",
			KeyToolUseID:             "c1",
			ArgPrefix + KeyTool:      "sneaky",
			ArgPrefix + KeyEffectID:  "spoofed",
			ArgPrefix + KeyToolUseID: "hijacked",
		}},
	}

	call := findToolUse(DeriveMessages(events)[0])

	if call.Name != "read" {
		t.Fatalf("tool name = %q, want read", call.Name)
	}
	if call.ToolUseID != "c1" {
		t.Fatalf("tool use id = %q, want c1", call.ToolUseID)
	}
	if call.Input[KeyTool] != "sneaky" || call.Input[KeyEffectID] != "spoofed" {
		t.Fatalf("arguments lost: %+v", call.Input)
	}
}

func TestDeriveMessagesDropsEmptyAssistantMessage(t *testing.T) {
	events := []Event{
		{Type: EventUserInput, Data: map[string]string{KeyContent: "go"}},
		{Type: EventAssistantOutput, Data: map[string]string{KeyContent: ""}},
	}

	msgs := DeriveMessages(events)

	if len(msgs) != 1 {
		t.Fatalf("derived %d messages, want 1 (text-less assistant turn dropped): %+v", len(msgs), msgs)
	}
}

func TestDeriveMessagesRejectionBecomesErrorResult(t *testing.T) {
	events := []Event{
		{Type: EventAssistantOutput, Data: map[string]string{KeyContent: "call"}},
		{Type: EventEffectIntent, Data: map[string]string{KeyTool: "write", KeyToolUseID: "c1"}},
		{Type: EventEffectRejected, Data: map[string]string{
			KeyToolUseID: "c1", KeyReason: "process lacks permission: tool:write",
		}},
	}

	msgs := DeriveMessages(events)

	result := msgs[1].Content[0].ToolResult
	if result.Status != StatusError {
		t.Fatalf("status = %q, want %q", result.Status, StatusError)
	}
	if toolResultText(result) != "process lacks permission: tool:write" {
		t.Fatalf("result text = %q", toolResultText(result))
	}
}

// The rich-handler metadata event is audit-only: it must not become a second
// tool result for the same call.
func TestDeriveMessagesIgnoresMetadataEvent(t *testing.T) {
	events := []Event{
		{Type: EventAssistantOutput, Data: map[string]string{KeyContent: "call"}},
		{Type: EventEffectIntent, Data: map[string]string{KeyTool: "read", KeyToolUseID: "c1"}},
		{Type: EventEffectMetadata, Data: map[string]string{KeyToolUseID: "c1", "meta.source": "disk"}},
		{Type: EventEffectObserved, Data: map[string]string{KeyToolUseID: "c1", KeyResult: "data"}},
	}

	msgs := DeriveMessages(events)

	if len(msgs[1].Content) != 1 {
		t.Fatalf("derived %d results for one call, want 1", len(msgs[1].Content))
	}
}

// A cache hit must still be observed, or the transcript leaves the second
// identical call unanswered.
func TestCachedEffectIsStillAnswered(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		if ctx.Step > 1 {
			return Proposal{Text: "done"}
		}
		return Proposal{
			Text: "reading twice",
			Effects: []ProposedEffect{
				{Tool: "read", Args: map[string]string{"path": "/tmp/x"}, ToolUseID: "c1"},
				{Tool: "read", Args: map[string]string{"path": "/tmp/x"}, ToolUseID: "c2"},
			},
		}
	})
	readTool := Tool{
		Name:    "read",
		Handler: func(args map[string]string) (string, error) { return "data", nil },
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("agent", "", []Capability{"read"}, "")
	p.Tools = []ToolDefinition{{Name: "read"}}
	p.View.Filter = func(e Event) bool { return e.ProcessID == p.ID }
	wf.Inject(p.ID, "go")
	wf.Step(context.Background(), p.ID)

	msgs := DeriveMessages(visibleEvents(p))
	answered := make(map[string]bool)
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.ToolResult != nil {
				answered[b.ToolResult.ToolUseID] = true
			}
		}
	}
	if !answered["c1"] || !answered["c2"] {
		t.Fatalf("answered calls = %+v, want both c1 and c2", answered)
	}
}

// Parallel dispatch must not reorder the derived transcript. The first tool
// blocks until the second finishes, so completion order is the reverse of model
// order.
func TestDeriveMessagesModelOrderUnderParallelDispatch(t *testing.T) {
	secondDone := make(chan struct{})
	slow := Tool{
		Name:           "slow",
		Parallelizable: true,
		Handler: func(args map[string]string) (string, error) {
			<-secondDone
			return "slow-result", nil
		},
	}
	fast := Tool{
		Name:           "fast",
		Parallelizable: true,
		Handler: func(args map[string]string) (string, error) {
			close(secondDone)
			return "fast-result", nil
		},
	}

	gen := GeneratorFunc(func(ctx Context) Proposal {
		if ctx.Step > 1 {
			return Proposal{Text: "done"}
		}
		return Proposal{
			Text: "both",
			Effects: []ProposedEffect{
				{Tool: "slow", Args: map[string]string{}, ToolUseID: "c_slow"},
				{Tool: "fast", Args: map[string]string{}, ToolUseID: "c_fast"},
			},
		}
	})

	wf, err := NewWorkflowWithOptions(gen, WithTools(slow, fast), WithEffectConcurrency(2))
	if err != nil {
		t.Fatal(err)
	}
	p := wf.Spawn("agent", "", []Capability{"slow", "fast"}, "")
	p.Tools = []ToolDefinition{{Name: "slow"}, {Name: "fast"}}
	p.View.Filter = func(e Event) bool { return e.ProcessID == p.ID }
	wf.Inject(p.ID, "go")
	wf.Step(context.Background(), p.ID)

	msgs := DeriveMessages(visibleEvents(p))
	var order []string
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.ToolResult != nil {
				order = append(order, b.ToolResult.ToolUseID)
			}
		}
	}
	if len(order) != 2 || order[0] != "c_slow" || order[1] != "c_fast" {
		t.Fatalf("result order = %+v, want [c_slow c_fast]", order)
	}
}

func findToolUse(m Message) *ToolUse {
	for _, b := range m.Content {
		if b.ToolUse != nil {
			return b.ToolUse
		}
	}
	return nil
}
