package orchid

import "strings"

// BuildContext extracts structured context from a process's log view.
func BuildContext(p *Process) Context {
	events := visibleEvents(p)

	ctx := Context{
		Spec:   p.Spec,
		Events: events,
		Tools:  p.Tools,
	}

	for _, e := range events {
		switch e.Type {
		case EventUserInput:
			ctx.LastInput = e.Data["content"]
			ctx.Step++
		case EventAssistantOutput:
			ctx.Step++
		case EventEffectObserved:
			ctx.ToolResults = append(ctx.ToolResults, ToolResultEntry{
				Tool:   e.Data["tool"],
				Result: e.Data["result"],
			})
		case EventJoin:
			ctx.ChildResults = append(ctx.ChildResults, ChildResultEntry{
				ChildID: e.Data["child_id"],
				Result:  e.Data["result"],
			})
		}
	}

	return ctx
}

// RenderContext serializes structured context to a text prompt for LLMs.
func RenderContext(ctx Context) string {
	var b strings.Builder
	b.WriteString("System: ")
	b.WriteString(ctx.Spec.SystemPrompt)
	b.WriteString("\n\n")

	for _, e := range ctx.Events {
		switch e.Type {
		case EventUserInput:
			b.WriteString("User: ")
			b.WriteString(e.Data["content"])
			b.WriteString("\n")
		case EventAssistantOutput:
			b.WriteString("Assistant: ")
			b.WriteString(e.Data["content"])
			b.WriteString("\n")
		case EventEffectObserved:
			b.WriteString("Tool [")
			b.WriteString(e.Data["tool"])
			b.WriteString("]: ")
			b.WriteString(e.Data["result"])
			b.WriteString("\n")
		case EventMessage:
			b.WriteString("Message from ")
			b.WriteString(e.Data["from"])
			b.WriteString(": ")
			b.WriteString(e.Data["content"])
			b.WriteString("\n")
		case EventJoin:
			b.WriteString("Child result [")
			b.WriteString(e.Data["child_id"])
			b.WriteString("]: ")
			b.WriteString(e.Data["result"])
			b.WriteString("\n")
		}
	}

	return b.String()
}

func visibleEvents(p *Process) []Event {
	all := p.View.Source.All()
	var out []Event
	for _, e := range all {
		if e.ID < p.View.StartCut {
			continue
		}
		if p.View.Filter != nil && !p.View.Filter(e) {
			continue
		}
		out = append(out, e)
	}
	return out
}
