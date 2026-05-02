package orchid

type Param struct {
	Type        string
	Description string
	Required    bool
}

// Tool describes a callable function an agent can invoke.
// Exactly one of Handler, ContextualHandler, or RichHandler must be set.
type Tool struct {
	Name        string
	Description string
	Params      map[string]Param

	// Deprecated: prefer ContextualHandler or RichHandler for new tools.
	Handler EffectHandler

	ContextualHandler ContextualEffectHandler

	RichHandler RichEffectHandler

	// Parallelizable marks the tool safe for concurrent execution.
	Parallelizable bool
}

// With returns a copy of the tool with per-tool middleware folded into the handler.
// Per-tool middleware runs inside workflow-global middleware.
func (t Tool) With(mw ...ToolMiddleware) Tool {
	var base ContextualEffectHandler
	switch {
	case t.RichHandler != nil:
		base = adaptRichHandler(t.RichHandler)
	case t.ContextualHandler != nil:
		base = t.ContextualHandler
	default:
		base = adaptLegacyHandler(t.Handler)
	}
	t.ContextualHandler = applyMiddleware(base, mw)
	t.Handler = nil
	t.RichHandler = nil
	return t
}
