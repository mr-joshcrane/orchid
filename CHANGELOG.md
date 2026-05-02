# Changelog

All notable changes to this project will be documented in this file.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased]

### Added — Tool middleware + orchidx subpackage (Phase 1)

- `ToolMiddleware` func type for wrapping `ContextualEffectHandler`.
- `Workflow.Use(mw ...ToolMiddleware)` — workflow-global middleware applied to every tool at dispatch time. Panics if called after `Run` starts.
- `Tool.With(mw ...ToolMiddleware) Tool` — per-tool middleware folded into the handler at construction time (runs inside global middleware).
- `HandlerContext.ToolName` field — populated at dispatch time for middleware that needs to identify the tool.
- `EventOutputTruncated` event type.
- `orchidx` subpackage (`github.com/mr-joshcrane/orchid/orchidx`) with four middleware:
  - `orchidx.CapOutput(maxBytes int)` — truncates output with machine-readable marker, emits `EventOutputTruncated`.
  - `orchidx.Timeout(d time.Duration)` — wraps handler context with deadline, returns `TOOL_TIMEOUT` error on expiry.
  - `orchidx.Redact(patterns ...*regexp.Regexp)` — replaces regex matches in output with `[REDACTED]`.
  - `orchidx.Meter(sink MetricSink)` — records tool name, duration, bytes in/out, and error to a `MetricSink` interface.

### Added — internal: options API, handler context, parallelism flag (Phase 0)

- `WorkflowOption` func type and `NewWorkflowWithOptions(gen, ...WorkflowOption)` constructor.
- `WithTools(...Tool) WorkflowOption` — registers tools via the options form.
- `HandlerContext` struct carrying `Ctx`, `ProcessID`, `RootDir` (empty until Phase 3), and `LogAppend` closure.
- `ContextualEffectHandler` — new preferred handler signature receiving `HandlerContext`.
- `Tool.ContextualHandler` field — preferred alternative to `Tool.Handler`.
- `Tool.Parallelizable` bool field — consumed in Phase 6 (`WithEffectConcurrency`).
- `EffectTable.RegisterTool(Tool) error` — dual-dispatch registration; enforces exactly-one-handler rule.
- `EffectTable.GetContextualHandler(string)` — lookup for the new handler type.
- `Workflow.invokeHandler` — internal helper that dispatches to contextual or legacy handler.

### Changed — internal

- `processEffect` now accepts `context.Context` and dispatches to `ContextualEffectHandler` when registered, falling back to legacy `EffectHandler`.
- `Tool` and `Param` types moved from `display.go` to new `tool.go`.

### Deprecated

- `Tool.Handler` — kept working; prefer `Tool.ContextualHandler` for new tools.

### Unchanged

- `NewWorkflow(gen, ...Tool)` — existing constructor and all call sites untouched.
- `EffectTable.RegisterHandler(name, EffectHandler)` — kept as-is for direct table use.
