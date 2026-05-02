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
