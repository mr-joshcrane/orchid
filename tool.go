package orchid

type Param struct {
	Type        string
	Description string
	Required    bool
}

// Tool describes a callable function an agent can invoke.
// Exactly one of Handler or ContextualHandler must be set;
// setting both or neither is a registration error.
type Tool struct {
	Name        string
	Description string
	Params      map[string]Param

	// Deprecated: prefer ContextualHandler for new tools.
	Handler EffectHandler

	ContextualHandler ContextualEffectHandler

	// Parallelizable marks the tool safe for concurrent execution.
	// Consumed in Phase 6 (WithEffectConcurrency).
	Parallelizable bool
}

// With returns a copy of the tool with per-tool middleware folded into the handler.
// Per-tool middleware runs inside workflow-global middleware.
func (t Tool) With(mw ...ToolMiddleware) Tool {
	base := t.ContextualHandler
	if base == nil {
		base = adaptLegacyHandler(t.Handler)
	}
	t.ContextualHandler = applyMiddleware(base, mw)
	t.Handler = nil
	return t
}
