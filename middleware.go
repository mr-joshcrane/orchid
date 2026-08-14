package orchid

// ToolMiddleware wraps a ContextualEffectHandler, returning a new one.
// Middleware ordering: the earliest Use call produces the outermost wrapper.
type ToolMiddleware func(next ContextualEffectHandler) ContextualEffectHandler

func adaptLegacyHandler(h EffectHandler) ContextualEffectHandler {
	return func(_ HandlerContext, args map[string]string) (string, error) {
		return h(args)
	}
}

func adaptRichHandler(h RichEffectHandler) ContextualEffectHandler {
	return func(hctx HandlerContext, args map[string]string) (string, error) {
		result, err := h(hctx, args)
		if err != nil {
			return "", err
		}
		if len(result.Metadata) > 0 {
			data := map[string]string{
				KeyTool:      hctx.ToolName,
				KeyEffectID:  hctx.EffectID,
				KeyToolUseID: hctx.ToolUseID,
			}
			for k, v := range result.Metadata {
				data["meta."+k] = v
			}
			if result.MIME != "" {
				data["mime"] = result.MIME
			}
			hctx.LogAppend(Event{
				Type:      EventEffectMetadata,
				ProcessID: hctx.ProcessID,
				Data:      data,
			})
		}
		return result.Content, nil
	}
}

func applyMiddleware(base ContextualEffectHandler, mws []ToolMiddleware) ContextualEffectHandler {
	h := base
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
