# orchid

An agent runtime for Go, built on one idea: **the event log is the only state.**

Nothing stores the conversation. Every turn, the transcript sent to the model is
projected from an append-only log of what happened — `DeriveMessages(events)`.
Tool calls, their results, permission refusals, and context-pressure telemetry
are all events, and the log is the audit trail, the transcript, and the replay
source at once.

That single decision is what buys the rest:

- **Parallel tool calls stay provider-valid.** Call intents are logged before
  dispatch, so results are ordered by their position in the assistant message,
  not by which handler finished first.
- **Every call is answered.** A batch abandoned partway still records a result
  for each planned call, so a transcript never holds a dangling `tool_use`.
- **Runs replay.** A log written to disk reads back into the identical
  transcript (`ReadEvents` → `DeriveMessages`).

## Quickstart

```go
text, usage, err := orchid.MustRun(ctx, gen, "analyst", "Summarise this project.",
    orchid.WithRootDir(projectDir),
    orchid.WithLogDir("logs"),
    orchid.WithTools(
        orchid.ReadTool("."),
        orchid.ListTool("."),
        orchid.SearchTool("."),
    ),
)
```

`MustRun` builds a workflow, runs it, and always closes it — flushing the JSONL
sink and writing a human-readable `audit.txt` plus a `tree.txt` of the process
hierarchy. A failure to write those reaches you in `err`.

For more control, `NewWorkflowWithOptions` is the constructor:

```go
wf, err := orchid.NewWorkflowWithOptions(gen,
    orchid.WithRootDir(projectDir),
    orchid.WithTools(readTool, writeTool.With(orchidx.Timeout(5*time.Second))),
    orchid.WithEffectConcurrency(4),
)
if err != nil {
    return err
}
defer wf.Close()
```

A tool that sets zero or multiple handlers is a configuration error returned
here, not a panic.

## Tools

Built in: `ReadTool`, `ReadBytesTool`, `SearchTool`, `ListTool`. All are
read-only and confined to the workflow's root directory; `read` paginates by
line (`line_start`), `read_bytes` by byte range (`offset`, `length`).

A tool sets exactly one handler:

| Field | Signature | Use when |
|---|---|---|
| `Handler` | `func(args) (string, error)` | the tool needs nothing from the runtime |
| `ContextualHandler` | `func(HandlerContext, args) (string, error)` | you need `RootDir` or to append events |
| `RichHandler` | `func(HandlerContext, args) (Result, error)` | you want metadata in the audit log |

Set `Parallelizable: true` for tools safe to run concurrently; consecutive
parallelizable calls are batched up to `WithEffectConcurrency`.

## Permissions

Default deny. A process may call a tool only via a direct grant or a permission
bundle that expands to it — there is no path that admits a call the permission
set does not cover.

```go
wf.RegisterPermissionBundle(orchid.PermissionReadOnlyFS, "read", "list", "search")
```

Add admission policies for per-call decisions. They run on **every** call:

```go
wf.Effects.AddPolicy(func(p *orchid.Process, e *orchid.Effect) (bool, string) {
    if strings.HasPrefix(e.Args["path"], "/etc") {
        return false, "path is out of bounds"
    }
    return true, ""
})
```

Results are not memoized. A repeated identical call runs again — tools have
effects, so replaying an earlier result would report state that has since
changed and record an observation that never happened.

## Context policy

Compaction is a pure function of `(step, purpose, history)`, applied identically
across providers:

```go
p.ContextPolicy = orchidx.Chain(
    orchidx.EvictToolResults(3),      // keep the last 3, placehold the rest
    orchidx.PhasedPurpose(phases),    // swap the system prompt by step
)
```

`EvictToolResults` replaces old tool results with a byte-count placeholder while
preserving `tool_use`/`tool_result` pairing, so compaction cannot produce a
transcript a provider rejects.

## Middleware

```go
wf.Use(orchidx.CapOutput(32_000))                 // global, outermost
writeTool.With(orchidx.Timeout(5 * time.Second))  // per-tool, innermost
```

Also in `orchidx`: `Redact(patterns...)`, `Meter(sink)`, `ResolvePath`.

## Event log

Sinks receive every event as it is appended: `SetSink(w io.Writer)` for JSONL,
`AddSink` for additional consumers, `WithSQLiteLog(path)` for SQL-queryable
output (build with `-tags sqlite`).

Logs are versioned (`EventLogVersion`) and read back strictly — an unknown
field, an unrecognised event type, or a version from a newer build is an error
rather than a silent misread:

```go
events, err := orchid.ReadEvents(f)
messages := orchid.DeriveMessages(events)
```

## Generators

`BedrockGenerator` and `OpenAIGenerator` ship; a generator is any
`Generate(Context) Proposal`. Optional interfaces are probed and degrade
gracefully when absent: `ModelIdentifier`, `TokenEstimator`, `ModelLimiter`.

**Set your model's context window explicitly.** The library seeds a lookup table
but it cannot stay current with every provider release, and an unknown window
silently disables the prompt-pressure events:

```go
gen, err := orchid.NewBedrockGenerator("anthropic.claude-opus-5",
    orchid.BedrockWithContextLimit(1_000_000),
)
```

When a window is unknown, `EventPromptSize` records `model_limit_unknown` so the
gap is visible in the audit log rather than passing for a healthy zero.

## Not included

Stated plainly, because the absences are real:

- **No network perimeter.** Built-in tools are confined to `RootDir`, but
  nothing constrains where a custom tool dials. Use an `EffectPolicy`.
- **No sub-agents.** `Spawn`, the process table, and per-process log views exist
  and are exercised by tests, but no `spawn` tool is exposed, so a run is a
  single process.
- **No session resume.** Saving and restoring a run is a natural fit for a
  log-derived design, but the previous implementation persisted events without
  the configuration that produced them and would resume into a different tool
  set. It was removed rather than left to fail quietly.
- **No budget visible to the agent.** `Constraints{MaxSteps, MaxTokens}` bound a
  run, and pressure is logged, but the model is not told how much room is left.

## License

See repository.
