package orchidx

import (
	"strings"
	"testing"

	"github.com/mr-joshcrane/orchid"
)

func strPtr(s string) *string { return &s }

func TestPhasedPurpose_SelectsCorrectPhase(t *testing.T) {
	phases := []Phase{
		{Step: 0, Purpose: "phase-0"},
		{Step: 5, Purpose: "phase-5"},
		{Step: 10, Purpose: "phase-10"},
	}
	policy := PhasedPurpose(phases)

	tests := []struct {
		step int
		want string
	}{
		{0, "phase-0"},
		{3, "phase-0"},
		{5, "phase-5"},
		{9, "phase-5"},
		{10, "phase-10"},
		{99, "phase-10"},
	}

	for _, tt := range tests {
		got, _ := policy(tt.step, "original", nil)
		if got != tt.want {
			t.Errorf("step %d: got %q, want %q", tt.step, got, tt.want)
		}
	}
}

func TestPhasedPurpose_NoPhases_ReturnOriginal(t *testing.T) {
	policy := PhasedPurpose(nil)
	got, _ := policy(5, "original", nil)
	if got != "original" {
		t.Errorf("got %q, want %q", got, "original")
	}
}

func TestEvictToolResults_KeepsLastN(t *testing.T) {
	history := []orchid.Message{
		{Role: "user", Content: []orchid.ContentBlock{{Text: strPtr("hello")}}},
		{Role: "assistant", Content: []orchid.ContentBlock{{Text: strPtr("thinking")}}},
		{Role: "user", Content: []orchid.ContentBlock{{ToolResult: &orchid.ToolResult{
			ToolUseID: "t1", Content: []orchid.TextBlock{{Text: "result-1-big-payload"}}, Status: "success",
		}}}},
		{Role: "assistant", Content: []orchid.ContentBlock{{Text: strPtr("more thinking")}}},
		{Role: "user", Content: []orchid.ContentBlock{{ToolResult: &orchid.ToolResult{
			ToolUseID: "t2", Content: []orchid.TextBlock{{Text: "result-2-big-payload"}}, Status: "success",
		}}}},
		{Role: "assistant", Content: []orchid.ContentBlock{{Text: strPtr("final")}}},
		{Role: "user", Content: []orchid.ContentBlock{{ToolResult: &orchid.ToolResult{
			ToolUseID: "t3", Content: []orchid.TextBlock{{Text: "result-3-big-payload"}}, Status: "success",
		}}}},
	}

	policy := EvictToolResults(2)
	_, compacted := policy(10, "purpose", history)

	if len(compacted) != len(history) {
		t.Fatalf("message count changed: got %d, want %d", len(compacted), len(history))
	}

	firstResult := compacted[2].Content[0].ToolResult
	if firstResult.ToolUseID != "t1" {
		t.Errorf("toolUseID changed: got %q, want %q", firstResult.ToolUseID, "t1")
	}
	if !strings.Contains(firstResult.Content[0].Text, "evicted") {
		t.Errorf("first result should be evicted, got %q", firstResult.Content[0].Text)
	}

	secondResult := compacted[4].Content[0].ToolResult
	if strings.Contains(secondResult.Content[0].Text, "evicted") {
		t.Errorf("second result should be kept, got %q", secondResult.Content[0].Text)
	}

	thirdResult := compacted[6].Content[0].ToolResult
	if strings.Contains(thirdResult.Content[0].Text, "evicted") {
		t.Errorf("third result should be kept, got %q", thirdResult.Content[0].Text)
	}
}

func TestEvictToolResults_FewResults_NoEviction(t *testing.T) {
	history := []orchid.Message{
		{Role: "user", Content: []orchid.ContentBlock{{ToolResult: &orchid.ToolResult{
			ToolUseID: "t1", Content: []orchid.TextBlock{{Text: "payload"}}, Status: "success",
		}}}},
	}

	policy := EvictToolResults(5)
	_, compacted := policy(10, "purpose", history)

	got := compacted[0].Content[0].ToolResult.Content[0].Text
	if got != "payload" {
		t.Errorf("should not evict when under keepLast, got %q", got)
	}
}

func TestEvictToolResults_PreservesToolUseID(t *testing.T) {
	history := []orchid.Message{
		{Role: "user", Content: []orchid.ContentBlock{{ToolResult: &orchid.ToolResult{
			ToolUseID: "keep-this-id", Content: []orchid.TextBlock{{Text: strings.Repeat("x", 10000)}}, Status: "success",
		}}}},
		{Role: "assistant", Content: []orchid.ContentBlock{{Text: strPtr("ok")}}},
		{Role: "user", Content: []orchid.ContentBlock{{ToolResult: &orchid.ToolResult{
			ToolUseID: "t2", Content: []orchid.TextBlock{{Text: "small"}}, Status: "success",
		}}}},
	}

	policy := EvictToolResults(1)
	_, compacted := policy(10, "purpose", history)

	evicted := compacted[0].Content[0].ToolResult
	if evicted.ToolUseID != "keep-this-id" {
		t.Errorf("toolUseID must be preserved, got %q", evicted.ToolUseID)
	}
	if evicted.Status != "success" {
		t.Errorf("status must be preserved, got %q", evicted.Status)
	}
}

func TestChain_AppliesPoliciesInOrder(t *testing.T) {
	addSuffix := func(step int, purpose string, history []orchid.Message) (string, []orchid.Message) {
		return purpose + "-A", history
	}
	addMore := func(step int, purpose string, history []orchid.Message) (string, []orchid.Message) {
		return purpose + "-B", history
	}

	policy := Chain(addSuffix, addMore)
	got, _ := policy(0, "start", nil)

	if got != "start-A-B" {
		t.Errorf("got %q, want %q", got, "start-A-B")
	}
}
