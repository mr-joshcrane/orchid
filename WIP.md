## Orchid WIP — Issues, Learnings, and Required Changes

### What Orchid Is
A Go framework that models LLM agent execution as an OS kernel. Processes produce proposals, proposals contain effects (tool calls), effects get admitted/rejected by capabilities, then executed by handlers. Everything is logged to an append-only event log.

### What We Built With It
A vulnerability triage tool that:
1. Fetches untriaged vulns from DynamoDB
2. Groups by vuln, writes investigation stubs
3. Processes by repo (clone once, investigate all vulns)
4. Parent agent generates recon commands, children run them and classify
5. Results stream to the user for accept/reject
6. Applies accepted triages to DynamoDB

### Bedrock Conversation History Was Fundamentally Broken
The original `buildMessages` reconstructed the Bedrock conversation from the event log. This was lossy:
- `toolUse` blocks (with `toolUseId`) were stripped from assistant messages
- Tool results were sent as plain text `[tool bash result]: ...` instead of proper `toolResult` content blocks with matching `toolUseId`
- Multiple consecutive user messages violated Bedrock's alternation requirement

This caused agents to hang mid-investigation. The conversation was invalid from Bedrock's perspective.

**Fix applied**: The Bedrock generator now maintains its own `[]bedrockMessage` history via `ctx.History` (stored as `any` on Process). Raw assistant messages (with toolUse blocks) are preserved. Tool results are sent as proper `toolResult` blocks with matching IDs. The event log is just for auditing, not for conversation reconstruction.

**Remaining awkwardness**: `History` is `any` on both `Process` and `Context`. This is because the history format is provider-specific (Bedrock messages vs OpenAI messages) but stored on provider-agnostic types. It works but it's a type-safety hole.

### The Event Log Tries To Do Two Jobs
It's trying to be both:
1. An audit trail (what happened, when, in what order)
2. The source of truth for conversation state

It fails at (2) because provider-specific details (toolUseId, content block structure) can't be represented in `map[string]string`. The history-on-Process fix made the event log purely (1), which is correct, but a bunch of the event types (`EventEffectAdmitted`, `EventEffectStarted`, `EventEffectApplied`) are now redundant — they're audit noise from when the log was trying to be the conversation state.

### `map[string]string` Everywhere
Event data, effect args, tool results — all stringly typed. This forced `fmt.Sprint(v)` conversions on Bedrock tool inputs and means nested JSON args get flattened. Tool parameter schemas are also missing — `ToolDefinition` has `Name` and `Description` but no parameter schema, so Bedrock gets `{"properties":{}}` and has to guess args from the description.

### Tool Result Token Explosion
Multi-step agents accumulate conversation history. Each step sends all previous messages. Tool results (grep output, file contents) can be 4-8KB each. By step 17, the conversation is 65KB and every subsequent call resends all of it. One agent investigating one vuln in one repo consumed ~200K input tokens across 17 calls.

**Attempted fixes and their failures**:

1. **Truncation** (arbitrary char limits on old tool results): Failed because arbitrary cutoffs lose important data and the agent re-reads files it can't remember.

2. **Replace old tool results with the assistant's next response**: Failed because the assistant's response after tool results is NOT a summary of what was found — it's transition text like "Now let me check X next" or just another tool call with no text. Replacing 2 different tool results with the same vague sentence destroyed both pieces of data.

3. **Placeholder strings** ("see assistant analysis below"): Catastrophically failed. Agent couldn't reference any prior data, looped trying to re-read files, got the placeholder back, looped again forever.

**Current approach being tested**: Standalone summarisation LLM calls. After each tool executes, if the result is over 200 chars, a separate one-shot LLM call summarises it. This call receives the original task context (from the first UserInput event) plus the tool name and raw output. The summary replaces the raw result before it enters the conversation history. The main agent never sees the raw output — only the summary.

This adds one cheap LLM call per tool execution but should keep conversation history flat (no quadratic growth). Not yet validated.

### The `compactHistory` Function Is Dead Code
It's still in `bedrock.go` but no longer called. It was the failed attempt at post-hoc compaction. Should be removed.

### The OpenAI Generator Is Broken
`llmGenerator` still uses the old `buildMessages`-from-events pattern. It doesn't support the history mechanism, `PendingResults`, or `ToolUseID`. Nobody is using it but it compiles and is misleading. Either fix it or remove it.

### `RawMessage` on Proposal Is Vestigial
It was added to serialize the Bedrock response message for storage in the event log, then deserialize it back in `resp2msg`. With the history-on-Process approach, `resp2msg` still uses it but it's a roundabout path. The response message should just be appended to history directly in `parseResponse` without a JSON round-trip.

### Fork/Join Isn't Used By The Real Application
The triage tool uses raw goroutines with `sync.WaitGroup` and channels. `Fork/Join` creates child processes in Orchid's runtime with filtered log views — machinery that's unnecessary for parallel LLM calls with isolated agents. The concurrent `Join` we added early on (goroutines in `Group.Join()`) works but the whole process/log-view model is overkill for the actual use case.

### Error Handling Was Systematically Broken
Every function swallowed errors with defaults, logged warnings, or continued execution. Parse failures returned `T4Low / "Could not parse LLM response"` which would have been submitted to DynamoDB as real triage decisions. Clone failures were silently ignored. Auth failures (403/expired tokens) kept processing 1200 groups of git clones for nothing.

This was fixed by making every function return errors, removing all `os.Exit` calls outside `main()`, and adding auth error detection to stop immediately on credential failures.

### Trace File Writer Corruption Under Concurrency
The prompt trace writer (`promptTrace.save`) wrote files that got corrupted under concurrent load — 618 corrupt files in one run. Fixed with atomic writes (write to `.tmp`, rename to `.json`). The `compactHistory` function also mutated history in-place which could race if multiple goroutines shared a generator, but each agent creates its own history so this hasn't been a problem in practice.

### Effect Caching
Added to prevent the agent from re-executing the same tool call with the same args. Cache keyed on `tool:arg1=val1&arg2=val2`, stored on Process. Returns cached result with updated `ToolUseID` so Bedrock's conversation stays valid.

### What Needs To Happen Next
1. Validate the summarisation approach — does it reduce tokens without losing classification quality?
2. Remove dead code: `compactHistory`, `llmGenerator` (or fix it), `RawMessage` roundtrip
3. Add tool parameter schemas to `ToolDefinition`
4. Write agent event logs to disk (the audit trail we keep asking for but never write)
5. Consider whether the Process/Runtime/EventLog model is earning its keep or if a simpler Agent-with-history would suffice
