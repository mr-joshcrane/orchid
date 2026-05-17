package orchidx

import (
	"fmt"

	"github.com/mr-joshcrane/orchid"
)

type Phase struct {
	Step    int
	Purpose string
}

func PhasedPurpose(phases []Phase) orchid.ContextPolicy {
	return func(step int, purpose string, history []orchid.Message) (string, []orchid.Message) {
		for i := len(phases) - 1; i >= 0; i-- {
			if step >= phases[i].Step {
				return phases[i].Purpose, history
			}
		}
		return purpose, history
	}
}

func EvictToolResults(keepLast int) orchid.ContextPolicy {
	return func(step int, purpose string, history []orchid.Message) (string, []orchid.Message) {
		resultIndices := toolResultIndices(history)
		evictBefore := len(resultIndices) - keepLast
		if evictBefore <= 0 {
			return purpose, history
		}

		compacted := make([]orchid.Message, len(history))
		copy(compacted, history)

		for i := 0; i < evictBefore; i++ {
			idx := resultIndices[i]
			compacted[idx] = evictMessage(compacted[idx])
		}
		return purpose, compacted
	}
}

func Chain(policies ...orchid.ContextPolicy) orchid.ContextPolicy {
	return func(step int, purpose string, history []orchid.Message) (string, []orchid.Message) {
		for _, p := range policies {
			purpose, history = p(step, purpose, history)
		}
		return purpose, history
	}
}

func toolResultIndices(history []orchid.Message) []int {
	var indices []int
	for i, msg := range history {
		if msg.Role != "user" {
			continue
		}
		for _, block := range msg.Content {
			if block.ToolResult != nil {
				indices = append(indices, i)
				break
			}
		}
	}
	return indices
}

func evictMessage(msg orchid.Message) orchid.Message {
	evicted := orchid.Message{Role: msg.Role, Content: make([]orchid.ContentBlock, len(msg.Content))}
	for i, block := range msg.Content {
		if block.ToolResult == nil {
			evicted.Content[i] = block
			continue
		}
		summary := fmt.Sprintf("[evicted — %d bytes]", toolResultSize(block.ToolResult))
		evicted.Content[i] = orchid.ContentBlock{
			ToolResult: &orchid.ToolResult{
				ToolUseID: block.ToolResult.ToolUseID,
				Content:   []orchid.TextBlock{{Text: summary}},
				Status:    block.ToolResult.Status,
			},
		}
	}
	return evicted
}

func toolResultSize(tr *orchid.ToolResult) int {
	var n int
	for _, tb := range tr.Content {
		n += len(tb.Text)
	}
	return n
}
