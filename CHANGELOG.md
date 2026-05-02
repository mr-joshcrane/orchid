# Changelog

All notable changes to this project will be documented in this file.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased]

### Added — Paper cuts (Phase 8)

- `Result` struct with `Content`, `MIME`, `Bytes`, and `Metadata` fields for structured tool output.
- `RichEffectHandler` — third handler style returning `Result` instead of plain string. Metadata is preserved in audit events.
- `Tool.RichHandler` field — exactly one of `Handler`, `ContextualHandler`, or `RichHandler` must be set.
- `HistorySerializer` optional interface — generators can implement `MarshalHistory`/`UnmarshalHistory` for session portability.
- `BedrockGenerator` and `OpenAIGenerator` implement `HistorySerializer`.
- `Workflow.SaveSession(io.Writer) error` — serialises events, process table, and per-process history to JSON.
- `Workflow.LoadSession(io.Reader) error` — restores session state for resuming crashed runs.

### Added — Generator telemetry and graceful degradation (Phase 7)

- `TokenEstimator` optional interface — generators can implement `EstimateTokens(Context) int` for custom token estimation. Default heuristic: `chars/4`.
- `ModelLimiter` optional interface — generators can implement `ModelLimit() int` for context window size.
- `RegisterModelLimit(model, limit)` — register custom model context window sizes. Ships with hardcoded limits for common Claude and GPT models.
- `EventPromptSize` event — emitted before every `Generate` call with estimated tokens, model limit, and utilisation percentage.
- `EventPromptPressure` event — emitted at `>80%` (level "warn") and `>95%` (level "critical") context utilisation.
- `EventHistoryTruncated` event type (for use by overflow policies).
- `BedrockGenerator`: `BedrockWithRetry(attempts, backoff)` and `BedrockWithOverflowPolicy(policy)` options. Retries on 429/5xx, not on 400-class.
- `OpenAIGenerator`: `OpenAIWithRetry(attempts, backoff)`, `OpenAIWithOverflowPolicy(policy)`, `OpenAIWithModel(model)` options.
- `BedrockGenerator` and `OpenAIGenerator` implement `TokenEstimator`, `ModelLimiter`, and `ModelIdentifier`.
- `internal/overflow` package — shared `Retry` logic, `OverflowPolicy` type, `ExponentialBackoff`, `IsRetryable`.

### Added — Parallel effect dispatch (Phase 6)

- `WithEffectConcurrency(n int) WorkflowOption` — sets the maximum number of concurrent parallelizable effects per step (default: 1, sequential).
- `ErrEffectFatal` sentinel error — when a handler returns an error wrapping `ErrEffectFatal`, the parallel batch is cancelled via errgroup context.
- Parallel dispatch logic: consecutive `Parallelizable` effects are batched and run via `errgroup` with the configured limit. Non-parallelizable effects force a drain of the current batch before running serially. Result order is preserved regardless of completion order.
- `ReadTool`, `ListTool`, `SearchTool` now set `Parallelizable: true`.
- Process-level effect cache is now mutex-protected for concurrent access.
- New dependency: `golang.org/x/sync` (Google-maintained, for `errgroup`).

### Added — Default audit logging (Phase 5)

- `WithLogDir(dir string) WorkflowOption` — opens a timestamped `events.jsonl` sink in `dir`; creates the directory if missing.
- `Workflow.Close() error` — closes the jsonl file, writes `<timestamp>-audit.txt` (human-readable event log) and `<timestamp>-tree.txt` (process hierarchy). Callers should `defer wf.Close()`.
- `MustRun(ctx, gen, purpose, task, ...WorkflowOption) (string, TokenUsage, error)` — builds a workflow, runs it, and always closes (even on error). Errors from `Close()` are joined via `errors.Join`.

### Added — Permission split (Phase 4)

- `Permission` type, `ToolName` type, `PermissionFromTool(ToolName) Permission`.
- `PermissionReadOnlyFS` predefined bundle — transitively grants `tool:read`, `tool:list`, `tool:search`.
- `Process.Permissions` field — preferred over `Capabilities`. Auto-populated from `Capabilities` at spawn time.
- `Process.HasPermission(Permission) bool`.
- `Workflow.RegisterPermissionBundle(Permission, ...ToolName)` — user-defined permission bundles, expanded at check time.
- `EventPermissionFallback` event type — emitted when a tool is admitted via deprecated `Capability` fallback.
- Permission check at effect time: `Permissions` first (direct + bundle expansion), then `Capabilities` fallback.

### Deprecated — Phase 4

- `Capability` — kept working via fallback; prefer `Permission` for new code.

### Changed (breaking) — WithRootDir and path resolution (Phase 3)

- `NewWorkflow` and `NewWorkflowWithOptions` now return `(*Workflow, error)`.
- `ListTool` and `SearchTool` upgraded from `Handler` to `ContextualHandler` to support `RootDir` override.
- Built-in tools (`ReadTool`, `ListTool`, `SearchTool`) ignore their constructor `root` argument when `hctx.RootDir` is set. Emits a one-time `EventRootOverride` warning per tool.

### Added — WithRootDir and path resolution (Phase 3)

- `WithRootDir(dir string) WorkflowOption` — sets the canonical filesystem root for the workflow. Resolves to absolute path; rejects non-existent directories at construction time.
- `HandlerContext.RootDir` populated from workflow root at dispatch time.
- `EventRootOverride` event type — emitted when workflow root overrides a tool's constructor root.
- `orchidx.ResolvePath(hctx, rel)` — canonical path resolver that joins `RootDir` + relative path and rejects `..` escapes.

### Changed (breaking) — Byte budgets on built-in tools (Phase 2)

- `ReadTool` now caps output at 32KB by default. Use `ReadWithMaxBytes(0)` to disable.
- `ReadTool` now uses `ContextualHandler` (was `Handler`). Existing call sites using `ReadTool(root)` are unaffected.
- `ReadTool` rejects binary files by default. Use `ReadWithBinaryDetect(false)` to disable.
- `ReadTool`, `ListTool`, `SearchTool` accept functional options as variadic second argument (backward-compatible).

### Added — Byte budgets on built-in tools (Phase 2)

- `ReadOption`, `ListOption`, `SearchOption` functional option types.
- `ReadWithMaxBytes(n)`, `ReadWithBinaryDetect(b)`, `ReadWithOffsetSupport(b)` options for `ReadTool`.
- `ListWithMaxBytes(n)`, `ListWithMaxEntries(n)` options for `ListTool`.
- `SearchWithMaxBytes(n)`, `SearchWithMaxMatches(n)` options for `SearchTool`.
- `ReadTool` native pagination via `offset` and `length` parameters.
- Tool descriptions are generated dynamically from config, truthfully advertising limits.
- `ReadTool` emits `EventOutputTruncated` when output is truncated by byte cap.

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
