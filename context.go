package orchid

// BuildContext extracts structured context from a process's log view.
func BuildContext(p *Process) Context {
	events := visibleEvents(p)

	ctx := Context{
		Name:           p.Name,
		Purpose:        p.Purpose,
		Events:         events,
		Tools:          p.Tools,
		History:        p.History,
		PendingResults: p.PendingResults,
		ContextPolicy:  p.ContextPolicy,
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
		}
	}

	return ctx
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
