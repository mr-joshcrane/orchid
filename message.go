package orchid

// Message is a provider-neutral conversation entry. Generators convert these
// into their wire format; nothing outside a generator sees provider types.
//
// Role is "user" or "assistant". Tool results ride in a user message, matching
// the Anthropic/Bedrock convention; generators whose API models tool results as
// a distinct role split them during conversion.
type Message struct {
	Role    string
	Content []ContentBlock
}

// ContentBlock is one part of a Message. Exactly one field is non-nil.
type ContentBlock struct {
	Text       *string
	ToolUse    *ToolUse
	ToolResult *ToolResult
}

// ToolUse is a model-proposed tool call. ToolUseID pairs it with the
// ToolResult that answers it.
type ToolUse struct {
	ToolUseID string
	Name      string
	Input     map[string]string
}

// ToolResult answers the ToolUse carrying the same ToolUseID. Status is
// StatusSuccess or StatusError.
type ToolResult struct {
	ToolUseID string
	Content   []TextBlock
	Status    string
}

// TextBlock is a run of text inside a ToolResult.
type TextBlock struct {
	Text string
}

// Tool result statuses.
const (
	StatusSuccess = "success"
	StatusError   = "error"
)

// Message roles.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// TextContent builds a single-text-block content slice.
func TextContent(text string) []ContentBlock {
	return []ContentBlock{{Text: &text}}
}

// messageText concatenates a message's text blocks, ignoring tool calls and
// results.
func messageText(m Message) string {
	var text string
	for _, b := range m.Content {
		if b.Text == nil {
			continue
		}
		text += *b.Text
	}
	return text
}

// toolResultText concatenates a tool result's text blocks.
func toolResultText(r *ToolResult) string {
	var text string
	for _, t := range r.Content {
		text += t.Text
	}
	return text
}

// ContextPolicy rewrites the system prompt and conversation history before each
// LLM call. It receives the current step number, the purpose string, and the
// derived message history, and returns the (possibly rewritten) purpose and
// history. Every generator applies it, so a policy is provider-independent.
//
// Returning inputs unchanged is always safe. Policies must preserve ToolUseID
// continuity — every ToolUse in an assistant message must have a matching
// ToolResult in the following user message.
type ContextPolicy func(step int, purpose string, history []Message) (string, []Message)
