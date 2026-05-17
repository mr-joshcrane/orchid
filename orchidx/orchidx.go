package orchidx

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/mr-joshcrane/orchid"
)

type MetricSink interface {
	Observe(tool string, durMS int64, bytesIn int, bytesOut int, err error)
}

func CapOutput(maxBytes int) orchid.ToolMiddleware {
	return func(next orchid.ContextualEffectHandler) orchid.ContextualEffectHandler {
		return func(hctx orchid.HandlerContext, args map[string]string) (string, error) {
			result, err := next(hctx, args)
			if err != nil {
				return result, err
			}
			if len(result) <= maxBytes {
				return result, nil
			}
			total := len(result)
			truncated := result[:maxBytes]
			truncated += fmt.Sprintf("\n\n[orchid:truncated bytes_total=%d bytes_shown=%d]\n"+
				"IMPORTANT: This output is large and will be evicted from your context in a few steps. "+
				"State the key findings from this output in your next response so you retain them.", total, maxBytes)
			hctx.LogAppend(orchid.Event{
				Type:      orchid.EventOutputTruncated,
				ProcessID: hctx.ProcessID,
				Data: map[string]string{
					"bytes_total": strconv.Itoa(total),
					"bytes_shown": strconv.Itoa(maxBytes),
				},
			})
			return truncated, nil
		}
	}
}

func Timeout(d time.Duration) orchid.ToolMiddleware {
	return func(next orchid.ContextualEffectHandler) orchid.ContextualEffectHandler {
		return func(hctx orchid.HandlerContext, args map[string]string) (string, error) {
			ctx, cancel := context.WithTimeout(hctx.Ctx, d)
			defer cancel()
			hctx.Ctx = ctx
			type result struct {
				s   string
				err error
			}
			ch := make(chan result, 1)
			go func() {
				s, err := next(hctx, args)
				ch <- result{s, err}
			}()
			select {
			case <-ctx.Done():
				return "", fmt.Errorf("TOOL_TIMEOUT after %s", d)
			case r := <-ch:
				return r.s, r.err
			}
		}
	}
}

func Redact(patterns ...*regexp.Regexp) orchid.ToolMiddleware {
	return func(next orchid.ContextualEffectHandler) orchid.ContextualEffectHandler {
		return func(hctx orchid.HandlerContext, args map[string]string) (string, error) {
			result, err := next(hctx, args)
			if err != nil {
				return result, err
			}
			for _, re := range patterns {
				result = re.ReplaceAllString(result, "[REDACTED]")
			}
			return result, nil
		}
	}
}

func Meter(sink MetricSink) orchid.ToolMiddleware {
	return func(next orchid.ContextualEffectHandler) orchid.ContextualEffectHandler {
		return func(hctx orchid.HandlerContext, args map[string]string) (string, error) {
			var bytesIn int
			for k, v := range args {
				bytesIn += len(k) + len(v)
			}
			start := time.Now()
			result, err := next(hctx, args)
			durMS := time.Since(start).Milliseconds()
			sink.Observe(hctx.ToolName, durMS, bytesIn, len(result), err)
			return result, err
		}
	}
}
