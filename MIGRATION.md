# Migration Guide

## Phase 2 — ReadTool default byte cap (breaking)

`ReadTool` now caps output at 32KB by default. Previously it returned the entire file regardless of size.

**If you relied on unbounded reads:**

```go
// Before: unbounded
tool := orchid.ReadTool(root)

// After: opt out of cap
tool := orchid.ReadTool(root, orchid.ReadWithMaxBytes(0))
```

**If you want to keep the cap but need larger files:**

```go
// Use pagination
tool := orchid.ReadTool(root)
// Then in tool args: {"path": "big.txt", "offset": "32000", "length": "32000"}
```

## Phase 2 — ReadTool binary detection (new default)

`ReadTool` now rejects binary files by default, returning a `TOOL_REJECTED` message.

```go
// Disable binary detection
tool := orchid.ReadTool(root, orchid.ReadWithBinaryDetect(false))
```
