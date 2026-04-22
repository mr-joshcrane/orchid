# Audit Log Enrichment

## What Changed

### Cut: Redundant effect lifecycle events

Removed `EventEffectAdmitted`, `EventEffectStarted`, `EventEffectApplied`. A successful tool call now emits two events instead of five:

- `EventEffectIntent` — what tool was requested with what args
- `EventEffectObserved` — what came back (or `EventEffectRejected` — why it was blocked)

The in-memory `Effect` struct still tracks its internal state machine. The events are just the audit trail.

### Enriched: Token counts, model, duration on existing events

| Event | Added fields |
|---|---|
| `EventProcessSpawned` | `model`, `system_prompt` |
| `EventAssistantOutput` | `input_tokens`, `output_tokens`, `model` |
| `EventEffectObserved` | `duration_ms` |
| `EventJoin` | `input_tokens`, `output_tokens` (child totals) |

### Added: Process.Usage

Cumulative `TokenUsage` on each `Process`, updated after every `Step()`. Query without replaying the log.

### Added: EventLog.WriteTo + Agent.WriteLog

JSON Lines persistence. One event per line. All enriched fields included.

```go
f, _ := os.Create("audit.jsonl")
defer f.Close()
agent.WriteLog(f)
```

### Added: BuildProcessTree + PrintTree

Hierarchical process visualization with token rollup.

```go
roots := orchid.BuildProcessTree(agent.Log())
orchid.PrintTree(os.Stdout, roots)
```

Output:
```
coordinator (proc_1)  tokens=0/0  total=200/40
├── worker-0 (proc_2)  tokens=100/20  total=100/20  [1 steps, 0 effects]
└── worker-1 (proc_3)  tokens=100/20  total=100/20  [1 steps, 0 effects]
```

`tokens` = this process's own LLM calls. `total` = own + all children recursively.

## Breaking Changes

- `Runtime.Join` signature: added `TokenUsage` parameter
- `EventEffectAdmitted`, `EventEffectStarted`, `EventEffectApplied` removed from `EventType`
- `EventType` iota values shifted (affects anyone storing raw ints)

## What The Audit Trail Now Answers

| Question | How |
|---|---|
| What did each agent do? | Filter events by `ProcessID` |
| How much did it cost? | Sum `input_tokens`/`output_tokens` from `EventAssistantOutput`, or read `Process.Usage` |
| How much did the whole tree cost? | `BuildProcessTree` → `root.TotalUsage` |
| Which child was most expensive? | Compare `child.TotalUsage` across children |
| Why was a tool call rejected? | `EventEffectRejected` with `reason` |
| How long did a tool call take? | `EventEffectObserved` `duration_ms` |
| What model produced this output? | `EventAssistantOutput` `model` field |
| Persist for later analysis? | `agent.WriteLog(file)` → JSON Lines |
