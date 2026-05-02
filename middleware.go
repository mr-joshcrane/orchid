package orchid

// ToolMiddleware wraps a ContextualEffectHandler, returning a new one.
// Middleware ordering: the earliest Use call produces the outermost wrapper.
type ToolMiddleware func(next ContextualEffectHandler) ContextualEffectHandler

func adaptLegacyHandler(h EffectHandler) ContextualEffectHandler {
	return func(_ HandlerContext, args map[string]string) (string, error) {
		return h(args)
	}
}

func applyMiddleware(base ContextualEffectHandler, mws []ToolMiddleware) ContextualEffectHandler {
	h := base
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
