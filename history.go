package orchid

import "sort"

// Event data keys that carry derivation-significant values. Tool arguments are
// stored under ArgPrefix so they cannot collide with these.
const (
	KeyContent   = "content"
	KeyTool      = "tool"
	KeyToolUseID = "tool_use_id"
	KeyEffectID  = "effect_id"
	KeyResult    = "result"
	KeyReason    = "reason"

	// ArgPrefix namespaces tool arguments inside an effect event's Data map.
	ArgPrefix = "arg."
)

// DeriveMessages projects an event slice into the conversation the model sees.
// This is the single source of truth for history: an assistant message is an
// EventAssistantOutput plus the EventEffectIntent events that follow it, and
// tool results are the EventEffectObserved/EventEffectRejected events that
// answer them, grouped into one user message.
//
// Results are ordered by their tool call's position in the assistant message,
// not by log arrival, so parallel dispatch completing out of order still
// derives a provider-valid transcript. A result whose ToolUseID matches no
// preceding tool call is dropped: providers reject an unpaired tool result.
//
// Events that produce no message (process spawn, telemetry, truncation
// notices, rich-handler metadata) are ignored.
func DeriveMessages(events []Event) []Message {
	b := &historyBuilder{assistant: -1}
	for _, e := range events {
		b.add(e)
	}
	return b.finish()
}

// applyContextPolicy returns the purpose and history a generator should send,
// after the process's ContextPolicy has had its say. Every generator calls this
// so a policy behaves identically regardless of provider.
func applyContextPolicy(ctx Context) (string, []Message) {
	if ctx.ContextPolicy == nil {
		return ctx.Purpose, ctx.Messages
	}
	return ctx.ContextPolicy(ctx.Step, ctx.Purpose, ctx.Messages)
}

// historyBuilder folds events into messages, buffering tool results until the
// group they belong to is complete.
type historyBuilder struct {
	messages []Message
	// assistant indexes the open assistant message in messages, or -1.
	assistant int
	// callOrder maps a tool use id to its position in the open assistant message.
	callOrder map[string]int
	pending   []pendingResult
}

type pendingResult struct {
	result ToolResult
	// arrival preserves log order for results sharing a call position.
	arrival int
}

func (b *historyBuilder) add(e Event) {
	switch e.Type {
	case EventUserInput:
		b.flushResults()
		b.appendMessage(Message{Role: RoleUser, Content: TextContent(e.Data[KeyContent])})
	case EventAssistantOutput:
		b.flushResults()
		b.openAssistant(e.Data[KeyContent])
	case EventEffectIntent:
		b.addToolUse(e)
	case EventEffectObserved:
		b.bufferResult(e, e.Data[KeyResult], StatusSuccess)
	case EventEffectRejected:
		b.bufferResult(e, e.Data[KeyReason], StatusError)
	}
}

// appendMessage closes the open assistant message and appends msg.
func (b *historyBuilder) appendMessage(msg Message) {
	b.assistant = -1
	b.callOrder = nil
	b.messages = append(b.messages, msg)
}

// openAssistant starts an assistant message that later intents extend. Empty
// text yields a message with no text block; finish drops it if no tool call
// arrives, so a text-less step never injects an empty turn.
func (b *historyBuilder) openAssistant(text string) {
	msg := Message{Role: RoleAssistant}
	if text != "" {
		msg.Content = TextContent(text)
	}
	b.messages = append(b.messages, msg)
	b.assistant = len(b.messages) - 1
	b.callOrder = make(map[string]int)
}

// addToolUse appends a tool call to the open assistant message. An intent with
// no open assistant message cannot be paired and is ignored.
func (b *historyBuilder) addToolUse(e Event) {
	if b.assistant < 0 {
		return
	}
	id := e.Data[KeyToolUseID]
	b.callOrder[id] = len(b.callOrder)
	b.messages[b.assistant].Content = append(b.messages[b.assistant].Content, ContentBlock{
		ToolUse: &ToolUse{
			ToolUseID: id,
			Name:      e.Data[KeyTool],
			Input:     extractArgs(e.Data),
		},
	})
}

func (b *historyBuilder) bufferResult(e Event, content, status string) {
	b.pending = append(b.pending, pendingResult{
		result: ToolResult{
			ToolUseID: e.Data[KeyToolUseID],
			Content:   []TextBlock{{Text: content}},
			Status:    status,
		},
		arrival: len(b.pending),
	})
}

// flushResults emits the buffered results as one user message, ordered by their
// tool call's position in the assistant message that requested them.
func (b *historyBuilder) flushResults() {
	pending := b.pending
	b.pending = nil
	paired := make([]pendingResult, 0, len(pending))
	for _, p := range pending {
		if _, ok := b.callOrder[p.result.ToolUseID]; !ok {
			continue
		}
		paired = append(paired, p)
	}
	if len(paired) == 0 {
		return
	}
	order := b.callOrder
	sort.SliceStable(paired, func(i, j int) bool {
		return order[paired[i].result.ToolUseID] < order[paired[j].result.ToolUseID]
	})
	content := make([]ContentBlock, len(paired))
	for i, p := range paired {
		result := p.result
		content[i] = ContentBlock{ToolResult: &result}
	}
	b.appendMessage(Message{Role: RoleUser, Content: content})
}

// finish flushes the trailing result group and drops a trailing assistant
// message that carried neither text nor a tool call.
func (b *historyBuilder) finish() []Message {
	last := b.assistant
	b.flushResults()
	if last >= 0 && len(b.messages[last].Content) == 0 {
		return append(b.messages[:last], b.messages[last+1:]...)
	}
	return b.messages
}

// extractArgs recovers the tool arguments an effect event carries, stripping
// ArgPrefix and ignoring the event's own metadata keys.
func extractArgs(data map[string]string) map[string]string {
	args := make(map[string]string)
	for k, v := range data {
		if len(k) <= len(ArgPrefix) || k[:len(ArgPrefix)] != ArgPrefix {
			continue
		}
		args[k[len(ArgPrefix):]] = v
	}
	return args
}
