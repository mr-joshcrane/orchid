# SQLite Audit Log for Orchid — Implementation Plan

This is the spec a junior engineer should follow. Read it end-to-end before
touching any code. Where the original draft of this plan made a choice that
turned out to be wrong on closer inspection, the rationale is called out
inline.

## Goal

Add a SQLite sink to the existing `EventLog` so that the events orchid already
emits (currently JSONL + an `audit.txt` dump) can be queried with SQL. JSONL
and `audit.txt` stay untouched — we are *adding* a sink, not replacing one.
The primary consumer is downstream LLM-driven analysis of experiment runs.

## Out of scope

- Removing or reformatting JSONL / `audit.txt` output.
- A "queries shared by all runs" database. One DB per workflow, full stop.
  We can add a `runs` table later if needed.
- Reading SQLite back (e.g. session restore). Write-only sink.
- Migrations. Schema is created with `CREATE TABLE IF NOT EXISTS` and we
  bump a `PRAGMA user_version` if and when we change it.

## Background — what's actually there today

References are file:line so go check them first.

- `event.go:14-27` — 12 `EventType` constants. The full list of types is
  fixed and small.
- `event.go:44-50` — `Event{ID, Type, Timestamp, ProcessID, Data map[string]string}`.
  All payload is string-valued.
- `event.go:53-58` — `EventLog` holds `mu sync.RWMutex`, `events []Event`,
  `nextID uint64`, **`sink io.Writer`** (single sink, not a slice).
- `event.go:65-69` — `SetSink(io.Writer)`. Public API.
- `event.go:71-92` — `Append`. **Critical:** the sink write happens *while
  holding `l.mu` (write lock)*. Any new sink must therefore be non-blocking
  or fast, or we must restructure the lock. See §3.
- `runtime.go:131-149` — `WithLogDir` opens `<ts>-events.jsonl` and calls
  `SetSink(f)`.
- `runtime.go:189-213` — `Close()` closes `logFile`, then writes
  `<ts>-audit.txt` and `<ts>-tree.txt`.
- `runtime.go:352, 365, 409, 468, 479, 485, 657, 668, 678, 699, 713, 729,
  743, 777` — every `w.Log.Append(Event{...})` site. Skim them; the keys
  used in `Data` are the schema input. Notable keys actually emitted today:
  - Always: `Type`, `ProcessID`, `Timestamp`, `ID`.
  - `EventProcessSpawned`: `name`, `purpose`, `parent`.
  - `EventUserInput` / `EventAssistantOutput`: `content`, plus on
    assistant: `prompt_id`, `input_tokens`, `output_tokens`, `model`.
  - `EventEffectIntent`: `tool`, `effect_id`, **plus tool args merged in**
    (free-form keys from `pe.Args`).
  - `EventEffectObserved`: `effect_id`, `tool`, `result`, `duration_ms`.
  - `EventEffectRejected`: `effect_id`, `reason`, sometimes `duration_ms`.
  - `EventPromptSize` / `EventPromptPressure`: `estimated_tokens`,
    `model_limit`, `utilisation_pct`, `level`.
  - `EventPermissionFallback`: `tool`.
- `cmd/experiment/main.go:227-235` — current usage; we'll add
  `WithSQLiteLog(...)` alongside `WithLogDir`.
- `event_test.go`, `context_test.go` — many tests construct `EventLog`
  directly and call `Append`. **None** call `SetSink`. So we can change the
  internals freely; we just must not break the public signatures
  `NewEventLog`, `Append`, `Len`, `Slice`, `All`, `Since`, `WriteTo`,
  `SetSink`.
- `go.mod` / `go.sum` — no sqlite dependency yet, no `//go:build` tags
  used anywhere in the repo. We will introduce both.

## Decisions that differ from the first-pass draft

1. **Multi-sink, but keep `SetSink` semantics intact.** `SetSink` *replaces*
   the sole writer-sink slot today. If we silently turned it into "append
   to a list" we'd surprise anyone calling it twice. New API:
   `AddSink(EventSink)` for additive, `SetSink(io.Writer)` retains
   replace-the-writer semantics (it replaces only the implicit writer sink,
   not other registered sinks). See §1.
2. **Write outside the lock.** Holding `l.mu` (write lock) while doing a
   SQLite `INSERT` will serialise the entire workflow on disk I/O. Restructure
   `Append` to (a) take the lock, append in-memory, snapshot the event, drop
   the lock, then (b) fan out to sinks. Sinks must be internally
   thread-safe. See §3.
3. **Don't denormalise tool args into columns.** The junior's plan stored
   `tool, tokens_in, tokens_out, duration_ms, result` as columns. Fine.
   But it also planned to use `json_extract(data, '$.query')` for tool args
   — which only works if `data` is a JSON object string. We will store the
   full `Data` map as a JSON object in a `data` TEXT column (with
   `CHECK(json_valid(data))`), exactly so `json_extract` works. Tool args
   are *already* in `Data` for `EventEffectIntent` (see runtime.go:660,
   `mergeMap(..., pe.Args)`).
4. **Build tag `sqlite` for the *driver import*, not the public API.**
   `WithSQLiteLog` is always compiled and always linked. When built without
   the `sqlite` tag it returns an error from the option (sets
   `w.configErr`). Rationale: callers can compile-time depend on the symbol
   without being forced to add the tag, and CI can run the same code paths.
5. **Pure-Go driver.** `modernc.org/sqlite`. No CGo. Confirmed not yet in
   `go.mod`.
6. **One transaction per Close, batched writes.** A naive `INSERT` per
   event with `synchronous=NORMAL` is ~50µs each which is fine for
   experiments but wasteful. Use a single prepared statement and rely on
   WAL + `synchronous=NORMAL`. Do **not** wrap in an explicit transaction
   per write; do flush on `Close`. (If profiling shows a problem we can
   batch later — keep it simple first.)

## Files

| File | Status | What |
|------|--------|------|
| `event.go` | edit | Introduce `EventSink`, `AddSink`, internal `sinks []EventSink`, `writerSink` adapter. Restructure `Append` to release lock before fan-out. Keep all existing public methods. |
| `sqlite_sink.go` | new | `//go:build sqlite`. Real `SQLiteSink`. |
| `sqlite_sink_stub.go` | new | `//go:build !sqlite`. Returns `errSQLiteUnavailable` from `newSQLiteSink`. |
| `runtime.go` | edit | Add `WithSQLiteLog(path string) WorkflowOption`. Track sink on `Workflow`, close it in `Close()`. |
| `event_test.go` | edit | Add tests for multi-sink fan-out, `AddSink` ordering, that `SetSink` still works alone, that `Append` does not deadlock when a sink calls back into the log (regression for §3). |
| `sqlite_sink_test.go` | new | `//go:build sqlite`. End-to-end: open temp DB, append a representative event of each `EventType`, close, reopen the DB read-only, assert row counts and a few `json_extract` queries. |
| `cmd/experiment/main.go` | edit | Add `orchid.WithSQLiteLog(<path>.db)` next to existing `WithLogDir`. Path lives in the same per-run directory. |
| `go.mod` / `go.sum` | edit | `go get modernc.org/sqlite` under the `sqlite` tag (just `go mod tidy -tags sqlite`). |

## §1. EventSink interface and multi-sink fan-out

In `event.go`:

```go
type EventSink interface {
    Append(e Event)   // never blocks the caller for long; thread-safe
    Close() error
}

type writerSink struct {
    mu sync.Mutex
    w  io.Writer
}

func (s *writerSink) Append(e Event) {
    data, err := json.Marshal(eventRecord{ /* same fields as today */ })
    if err != nil { return }
    data = append(data, '\n')
    s.mu.Lock()
    s.w.Write(data) // best-effort, same as today
    s.mu.Unlock()
}

func (s *writerSink) Close() error { return nil }
```

`EventLog`:

```go
type EventLog struct {
    mu        sync.RWMutex
    events    []Event
    nextID    uint64
    writerIdx int            // -1 if none; index into sinks of the SetSink writer
    sinks     []EventSink
}
```

`SetSink` semantics (preserve back-compat):

- If `writerIdx >= 0` *replace* the entry at that index with a new
  `writerSink`.
- Otherwise append a new `writerSink` and record `writerIdx`.
- A `nil` writer removes the writer sink.

`AddSink`:

- Append to `sinks`. No special handling. Order matters only for tests.

## §2. Append restructure (drop lock before fan-out)

Today `Append` writes to the sink while holding the write lock. With a
SQLite sink that means every `INSERT` blocks every other event. Replace
the body of `Append` with:

```go
func (l *EventLog) Append(e Event) uint64 {
    l.mu.Lock()
    e.ID = l.nextID
    e.Timestamp = time.Now()
    l.nextID++
    l.events = append(l.events, e)
    sinks := l.sinks // slice header copy; safe to read after unlock because we never mutate elements in place
    l.mu.Unlock()

    for _, s := range sinks {
        s.Append(e)
    }
    return e.ID
}
```

Constraints this introduces, which the junior must respect:

- We treat the `sinks` slice as copy-on-write at the *header* level.
  Therefore `AddSink` and `SetSink` must allocate a new slice rather than
  `append` in place when capacity allows. Use:
  `l.sinks = append(append([]EventSink(nil), l.sinks...), s)`.
- Sinks are responsible for their own concurrency.
- Fan-out is best-effort and synchronous, in registration order. We do not
  swallow panics — a panicking sink should crash the workflow (matches
  current behaviour for the JSONL writer).

Add a regression test `TestEventLog_Append_DoesNotHoldLockDuringSinkWrite`
that registers a sink whose `Append` calls `log.Len()` and confirms it
returns without timing out.

## §3. SQLite sink (`sqlite_sink.go`, build tag `sqlite`)

```go
//go:build sqlite

package orchid

import (
    "database/sql"
    "encoding/json"
    "fmt"
    "sync"

    _ "modernc.org/sqlite"
)

type SQLiteSink struct {
    db   *sql.DB
    mu   sync.Mutex // serialises writes; sqlite is single-writer anyway
    stmt *sql.Stmt
}

func newSQLiteSink(path string) (*SQLiteSink, error) {
    dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", path)
    db, err := sql.Open("sqlite", dsn)
    if err != nil { return nil, err }
    if _, err := db.Exec(schemaSQL); err != nil {
        db.Close()
        return nil, err
    }
    stmt, err := db.Prepare(insertSQL)
    if err != nil {
        db.Close()
        return nil, err
    }
    return &SQLiteSink{db: db, stmt: stmt}, nil
}
```

Schema (constant string in same file):

```sql
CREATE TABLE IF NOT EXISTS events (
    id          INTEGER PRIMARY KEY,         -- mirrors Event.ID
    type        TEXT    NOT NULL,            -- EventType.String()
    timestamp   TEXT    NOT NULL,            -- RFC3339Nano
    process_id  TEXT    NOT NULL,
    tool        TEXT,                        -- Data["tool"]
    effect_id   TEXT,                        -- Data["effect_id"]
    result      TEXT,                        -- Data["result"] (may be large)
    tokens_in   INTEGER,                     -- Data["input_tokens"]
    tokens_out  INTEGER,                     -- Data["output_tokens"]
    duration_ms INTEGER,                     -- Data["duration_ms"]
    data        TEXT    NOT NULL CHECK(json_valid(data))
);
CREATE INDEX IF NOT EXISTS idx_events_type        ON events(type);
CREATE INDEX IF NOT EXISTS idx_events_process_id  ON events(process_id);
CREATE INDEX IF NOT EXISTS idx_events_tool        ON events(tool);
CREATE INDEX IF NOT EXISTS idx_events_effect_id   ON events(effect_id);
PRAGMA user_version = 1;
```

Why `effect_id` is its own column: every effect lifecycle event (intent,
rejected, observed) carries it, and joining intent→observed is the most
common query pattern.

`Append`:

```go
func (s *SQLiteSink) Append(e Event) {
    payload, err := json.Marshal(e.Data)   // {} when nil — emit "{}" not "null"
    if err != nil || string(payload) == "null" {
        payload = []byte("{}")
    }
    tokensIn  := atoiOrNil(e.Data["input_tokens"])
    tokensOut := atoiOrNil(e.Data["output_tokens"])
    durMs     := atoi64OrNil(e.Data["duration_ms"])
    s.mu.Lock()
    defer s.mu.Unlock()
    _, err = s.stmt.Exec(
        int64(e.ID),
        e.Type.String(),
        e.Timestamp.Format(time.RFC3339Nano),
        e.ProcessID,
        nullIfEmpty(e.Data["tool"]),
        nullIfEmpty(e.Data["effect_id"]),
        nullIfEmpty(e.Data["result"]),
        tokensIn, tokensOut, durMs,
        string(payload),
    )
    // best-effort, like the JSONL writer; do not panic
    _ = err
}
```

Helpers `atoiOrNil`, `atoi64OrNil`, `nullIfEmpty` return `interface{}` =
`sql.NullXxx{}` (or `nil`) when the input is empty / unparseable, so the
column stores SQL `NULL` and is properly indexable.

`Close`:

```go
func (s *SQLiteSink) Close() error {
    s.mu.Lock()
    defer s.mu.Unlock()
    var errs []error
    if s.stmt != nil { errs = append(errs, s.stmt.Close()); s.stmt = nil }
    if s.db   != nil { errs = append(errs, s.db.Close());   s.db   = nil }
    return errors.Join(errs...)
}
```

## §4. Stub for non-sqlite builds (`sqlite_sink_stub.go`)

```go
//go:build !sqlite

package orchid

import "errors"

var errSQLiteUnavailable = errors.New("orchid: SQLite sink not compiled in (rebuild with -tags sqlite)")

type SQLiteSink struct{}

func newSQLiteSink(string) (*SQLiteSink, error) { return nil, errSQLiteUnavailable }
func (*SQLiteSink) Append(Event)                {}
func (*SQLiteSink) Close() error                { return nil }
```

This keeps `WithSQLiteLog` always compilable.

## §5. `WithSQLiteLog` and `Close` wiring

In `runtime.go` add a field:

```go
type Workflow struct {
    // ...existing fields...
    sqliteSink *SQLiteSink
}
```

Option:

```go
func WithSQLiteLog(path string) WorkflowOption {
    return func(w *Workflow) {
        s, err := newSQLiteSink(path)
        if err != nil {
            w.configErr = fmt.Errorf("orchid: WithSQLiteLog(%q): %w", path, err)
            return
        }
        w.sqliteSink = s
        w.Log.AddSink(s)
    }
}
```

Extend `Close()` (around line 211). Add at the end, before the final
`return errors.Join(errs...)`:

```go
if w.sqliteSink != nil {
    errs = append(errs, w.sqliteSink.Close())
    w.sqliteSink = nil
}
```

Do **not** make `Close` idempotency depend on `w.logFile != nil` for the
sqlite branch — the user might call `WithSQLiteLog` without
`WithLogDir`. The sqlite branch must run independently.

## §6. Experiment integration

`cmd/experiment/main.go:233`. Right next to the existing `WithLogDir`:

```go
logRoot := fmt.Sprintf("/tmp/experiment-%s-%d", mode, time.Now().Unix())
baseTools = append(baseTools,
    orchid.WithLogDir(logRoot),
    orchid.WithSQLiteLog(filepath.Join(logRoot, "events.db")),
)
```

(The directory is created by `WithLogDir`. Order matters: `WithLogDir`
first, otherwise the directory may not exist when SQLite tries to open the
file. Document this ordering requirement in the godoc on `WithSQLiteLog`.)

The experiment binary should be built with `-tags sqlite`. Update any
build script / `Makefile` / `run-experiment.sh` that builds it. Confirm
via `grep -n 'go build\|go run' run-experiment.sh`.

## §7. Tests

### Unit (no build tag)

`event_test.go`, add:

- `TestEventLog_AddSink_FansOutInOrder` — register two recording sinks,
  `Append` once, assert both received it in registration order with the
  same `ID`.
- `TestEventLog_SetSink_ReplacesOnlyWriterSink` — `AddSink(custom)`,
  `SetSink(buf1)`, `SetSink(buf2)`. Append. Custom sink must have received
  the event; `buf1` must be empty; `buf2` must contain it.
- `TestEventLog_SetSink_Nil_RemovesWriter` — after `SetSink(nil)`, JSONL
  output stops; other sinks unaffected.
- `TestEventLog_Append_DoesNotHoldLockDuringSinkWrite` — sink whose
  `Append` calls `log.Len()`; must not deadlock. (Without the §3 fix this
  hangs.)
- Existing tests must still pass unchanged.

### SQLite (build tag `sqlite`)

`sqlite_sink_test.go`:

- `TestSQLiteSink_RoundTrip` — open `t.TempDir()/x.db`, append one event
  per `EventType` with realistic `Data`, `Close`, reopen with
  `database/sql`, assert `SELECT count(*) FROM events` matches and that
  `tool`, `tokens_in`, `duration_ms` columns are populated where expected
  and NULL otherwise.
- `TestSQLiteSink_JsonExtract` — append an `EventEffectIntent` with
  `Data["query"] = "foo"`, then
  `SELECT json_extract(data,'$.query') FROM events WHERE type='EffectIntent'`
  returns `"foo"`.
- `TestWithSQLiteLog_Close` — full `NewWorkflowWithOptions` happy path
  with `WithSQLiteLog`, run a trivial step, `Close`, assert the file
  exists and is non-empty.

### CI

Add a second test invocation (or a comment in `README` if there is no CI
config to edit): `go test -tags sqlite ./...`.

## §8. Acceptance checklist for the junior

Before opening a PR, all of these must be true:

1. `go build ./...` succeeds with no tag.
2. `go test ./...` passes with no tag.
3. `go build -tags sqlite ./...` succeeds.
4. `go test -tags sqlite ./...` passes.
5. `go vet ./...` clean.
6. `cmd/experiment` produces `events.db` in the per-run directory and
   `sqlite3 events.db 'select count(*) from events'` returns >0 after a
   short run.
7. `WithSQLiteLog` called without the `sqlite` build tag returns an error
   from `NewWorkflowWithOptions` (verified by a non-tagged test that
   inspects the returned error).
8. None of the existing `event_test.go` / `context_test.go` / `runtime_test.go`
   tests were modified except by addition.
9. `go.mod` only gains `modernc.org/sqlite` (and its transitive deps in
   `go.sum`).

## §9. Things explicitly NOT to do

- Don't introduce goroutine-buffered async writes "for performance".
  Premature; complicates `Close` ordering and event durability.
- Don't change `Event.Data` to `map[string]any`. Out of scope and breaks
  the world.
- Don't remove `audit.txt` or `tree.txt`. Humans still read them.
- Don't add a `runs` table or any cross-run schema. One DB per workflow.
- Don't try to replay JSONL into SQLite at `Close`. Sink writes are live;
  if the sink was registered late, it has fewer rows — that's fine and
  documented.

## §10. Suggested commit sequence

1. Refactor `event.go` to introduce `EventSink` + multi-sink fan-out + the
   §2 lock fix. Tests pass with no tag. **(no behaviour change)**
2. Add `sqlite_sink_stub.go` and `WithSQLiteLog` (returns error). Tests
   pass with no tag.
3. Add `sqlite_sink.go` + `sqlite_sink_test.go` under `//go:build sqlite`,
   add dep, run `go mod tidy -tags sqlite`. Tests pass with `-tags sqlite`.
4. Wire `cmd/experiment` and update build script.

Each commit independently buildable and reviewable.
