# orchid

Agent workflow library for Go.

## Middleware

Apply middleware globally to a workflow or per-tool:

```go
wf := orchid.NewWorkflowWithOptions(gen, orchid.WithTools(
    readTool,
    writeTool.With(orchidx.Timeout(5 * time.Second)),
))

// Global middleware — applies to every tool, outermost wrapper.
wf.Use(orchidx.CapOutput(32_000))
```

Available middleware in `orchidx`:

- `orchidx.CapOutput(maxBytes)` — truncates tool output to a byte limit
- `orchidx.Timeout(d)` — cancels tool execution after a deadline
- `orchidx.Redact(patterns...)` — replaces regex matches with `[REDACTED]`
- `orchidx.Meter(sink)` — records tool invocation metrics
