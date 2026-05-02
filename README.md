# orchid

Agent workflow library for Go.

## Quickstart

```go
text, usage, err := orchid.MustRun(ctx, gen, "analyst", "Summarise this project.",
    orchid.WithRootDir(caseDir),
    orchid.WithLogDir("logs"),
    orchid.WithTools(orchid.ReadTool("."), orchid.ListTool("."), orchid.SearchTool(".")),
)
```

`MustRun` builds a workflow, runs it, and always calls `Close()` — which flushes the jsonl event log, writes a human-readable `audit.txt`, and writes a `tree.txt` showing the process hierarchy.

## Middleware

Apply middleware globally to a workflow or per-tool:

```go
wf, err := orchid.NewWorkflowWithOptions(gen, orchid.WithTools(
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

## RootDir

Set a canonical filesystem root for the workflow. Built-in tools and `orchidx.ResolvePath` use it automatically:

```go
wf, err := orchid.NewWorkflowWithOptions(gen,
    orchid.WithRootDir("/path/to/project"),
    orchid.WithTools(orchid.ReadTool("."), orchid.ListTool(".")),
)

// In a custom tool handler:
resolved, err := orchidx.ResolvePath(hctx, args["path"])
```
