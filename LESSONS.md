## How This Should Have Been Built

### The Core Mistake
We built a vulnerability triage tool with LLM agents that investigate code, and at no point did we use our own audit system to verify what the agents were actually doing. We had `agent.Log()` and `PrintLog` from day one and never called them. Instead we wrote throwaway python scripts to parse raw JSON trace files, got partial information, made assumptions, and shipped broken code.

### What Should Have Happened

#### 1. Write audit logs from the start
The very first version of `classifyVulnInRepo` should have written `orchid.PrintLog(f, agent.Log())` to disk. Before any optimisation, before any parallelism, before any compaction. If we had done this, we would have seen:
- The `read` tool returning the first 8KB of poetry.lock (wrong section)
- Tool results being replaced with meaningless placeholder text
- Agents looping trying to re-read files they'd already seen
- The summariser confidently classifying based on data it never received

#### 2. Verify the first investigation end-to-end before scaling
We should have run ONE investigation on ONE repo, read the audit log, and confirmed:
- The right files were read
- The tool results contained the relevant data
- The classification referenced actual evidence from the tool output
- The justification matched what the tools found

Instead we parallelised across 5 repos immediately, then batched 5 groups, then ran 1200 groups unattended — all without ever verifying a single investigation produced correct results.

#### 3. Don't optimise what you haven't validated
We spent hours on:
- History compaction (replacing tool results with summaries)
- Standalone summarisation LLM calls
- Token counting and trace analysis
- Arbitrary truncation thresholds

All of this was premature. The tool results were wrong from the start (8KB from the wrong part of files). Compacting wrong data faster doesn't help.

#### 4. Error handling is not optional
Every function returned errors that got swallowed. Parse failures defaulted to T4Low. Clone failures were silently skipped. Auth failures kept processing. Each of these would have been caught immediately if we'd read the audit log after the first run.

The pattern of "log a warning and continue" is never acceptable when the output is a triage decision that gets written to a production database.

#### 5. The read tool is broken
The `read` tool handler returns `os.ReadFile` truncated to 8000 chars from the start. For a 50KB poetry.lock, you get packages A through AI. The agent asked to read the file to find langchain-openai at line 3818 — it never appeared in the output. This is the single most impactful bug and it was invisible because we never read the audit log.

The tool should either:
- Accept a line range (like `sed -n '3815,3830p'`)
- Accept a grep pattern (return only matching sections)
- Return the file in chunks with the agent requesting specific offsets

Or the recon commands (which use grep and are targeted) should be the primary data source, with `read` reserved for small files only.

#### 6. The summarisation approach was wrong three times
1. First attempt: truncate to arbitrary char limits. Lost important data.
2. Second attempt: replace with assistant's next response text. The assistant's response wasn't a summary — it was "let me check X next."
3. Third attempt: standalone summarisation LLM call. Doubled the call count, the summariser got wrong data (first 8KB of poetry.lock), and confidently produced incorrect summaries.

The correct approach: summarise tool results with a dedicated LLM call that receives the original task context. But this only works if the tool results contain the right data in the first place.

#### 7. Test with real data before deploying
252 triage decisions were written to DynamoDB. We checked them after the fact and they looked reasonable. But we never verified that the agents actually found the evidence they cited. The justifications sounded good because the LLM is good at writing plausible text, not because the evidence was real.

### What The Tool Should Look Like

```
triage --user josh@example.com --auto

Fetching 12000 vulns...
Grouped into 1200 groups across 600 repos

Processing repo accounts-service (8 vulns)...
  Cloned. Running recon...
  GHSA-xxx lodash → T4Low (transitive dev dep, no direct usage)
  GHSA-yyy express → T3Medium (used in app.js:42, handles user input)
  Audit log: ~/.triage/audit/accounts-service/

Processing repo ai-coach (5 vulns)...
  Cloned. Running recon...
  GHSA-r7w7 langchain-openai → T4Low (get_num_tokens never called)
  Audit log: ~/.triage/audit/ai-coach/

Done. 1200 triages in ~/.triage/
Run: triage --apply to submit to DynamoDB
Run: cat ~/.triage/audit/ai-coach/GHSA-r7w7.log to inspect any decision
```

Each audit log shows every event: what was asked, what tools ran, what they returned, what the LLM concluded. Readable. Verifiable. No python scripts needed.
