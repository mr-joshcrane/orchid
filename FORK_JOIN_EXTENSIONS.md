# Fork/Join Extensions

## Problem

The triage tool bypasses Orchid's Fork/Join because the current API is too narrow. Three missing capabilities forced raw goroutines:

1. **Per-child tools** — each child agent needs tools scoped to a different cloned repo directory. Fork gives every child the parent's tools.
2. **Multi-step execution** — `Group.Join()` calls `rt.Step()` once per child. Real agents loop until the LLM stops making tool calls.
3. **Streaming results** — `Join()` blocks until all children finish. The interactive flow needs results as they arrive.

These are configurations of the same workflow ("fan out N agents, fan in results"), not categorically different patterns. The fix is to widen Fork/Join, not maintain a parallel concurrency model outside the framework.

Every goroutine that bypasses the runtime loses audit logging, capability checks, and effect policies — the entire reason the kernel abstraction exists.

## Design Philosophy

Following goracle's API style:

- **One type, semantic methods** — configure the Agent, then call the verb (`Fork`).
- **Imperative mutation, not config structs** — `agent.Can(tools...)`, `agent.System("...")`, not `Task{Tools: ..., System: ...}`.
- **Private fields, public methods** — Group internals are hidden. Interact through `Join()` and `Stream()`.
- **Variadic for flexibility** — `Fork(prompts ...string)` for the simple case.
- **The simple case stays simple** — zero-config Fork/Join looks identical to today.

The key insight: instead of a `Task` config struct, each child is an `Agent`. Agents already know how to hold tools, system prompts, and configuration. Fork takes agents.

## Breaking Changes

- `Fork([]string)` replaced by `Fork(prompts ...string)` (variadic) and `ForkAgents(agents ...*Agent)`.
- `Join()` returns `([]Result, error)` instead of `([]string, error)`.
- `Result` struct replaces raw string returns.

## New Types

```go
// Result captures one child's outcome.
type Result struct {
    Index int
    Text  string
    Usage TokenUsage
    Err   error
}
```

That's it. One new exported type.

`StepCallback` is a type but it's just a function signature — no structs to learn:

```go
type StepCallback func(step int, text string, results []EffectResult) (inject string, stop bool)
```

## API After

### Simple — same prompt, same tools

```go
a := orchid.NewFromGenerator(gen)
a.Can(readTool, bashTool)
a.System("Analyze the file.")

results, err := a.Fork("Analyze file A", "Analyze file B", "Analyze file C").Join()
for _, r := range results {
    fmt.Println(r.Text)
}
```

Children inherit the parent's tools and system prompt. Variadic strings — same ergonomics as goracle's `Ask(question, references...)`.

### Per-child tools — each child is its own Agent

```go
childA := orchid.NewFromGenerator(gen)
childA.Can(repoTools(clonePathA)...)
childA.System("Security analyst. Be decisive.")
childA.GiveTask("Investigate repo A")

childB := orchid.NewFromGenerator(gen)
childB.Can(repoTools(clonePathB)...)
childB.System("Security analyst. Be decisive.")
childB.GiveTask("Investigate repo B")

results, err := orchid.ForkAgents(childA, childB).Join()
```

No config struct. Each child is configured the goracle way — imperative method calls. `ForkAgents` is a package-level function (not on Agent) because there's no parent here, just peers.

### Step budget

```go
a := orchid.NewFromGenerator(gen)
a.Can(tools...)
a.System("Security analyst.")

group := a.Fork("Investigate this vuln")
group.OnStep(func(step int, text string, results []orchid.EffectResult) (string, bool) {
    remaining := 10 - step
    if remaining <= 0 {
        return "No tool calls remaining. Classify now.", true
    }
    return fmt.Sprintf("%d tool calls remaining.", remaining), false
})

results, err := group.Join()
```

`OnStep` is a method on Group — imperative, not a config field. Set it between `Fork` and `Join`, or don't.

### Streaming (interactive)

```go
for r := range a.Fork(tasks...).Stream() {
    fmt.Printf("[%d] %s\n", r.Index, r.Text)
    // prompt user accept/reject
}
```

### MaxSteps

```go
group := a.Fork("Investigate this vuln")
group.SetMaxSteps(10)
results, err := group.Join()
```

Default is 50. Set it if you want a different budget. Imperative mutation like goracle's `SetPurpose`.

## The Full Public API Surface

```go
// On Agent (existing, unchanged)
func NewFromGenerator(gen Generator) *Agent
func (a *Agent) Can(tools ...Tool) *Agent
func (a *Agent) System(purpose string) *Agent
func (a *Agent) GiveTask(task string)
func (a *Agent) Step() (string, []EffectResult, error)

// On Agent (changed)
func (a *Agent) Fork(prompts ...string) *Group     // was Fork([]string), now variadic

// Package-level (new)
func ForkAgents(agents ...*Agent) *Group            // for per-child configuration

// On Group (new)
func (g *Group) OnStep(cb StepCallback)             // set step callback
func (g *Group) SetMaxSteps(n int)                  // set step limit (default 50)
func (g *Group) Join() ([]Result, error)             // batch collect
func (g *Group) Stream() <-chan Result               // streaming collect

// Types (new)
type Result struct { Index int; Text string; Usage TokenUsage; Err error }
type StepCallback func(step int, text string, results []EffectResult) (inject string, stop bool)
```

12 items in the public API. Small.

## Implementation

Five steps, each compiles and tests independently.

### Step 1: Per-process handler overrides

Add handler map to Process. Change processEffect to check it first.

**process.go** — add field and method:

```go
type Process struct {
    // ... existing fields
    Handlers map[string]EffectHandler
}

func (p *Process) GetHandler(tool string) (EffectHandler, bool) {
    if p.Handlers == nil {
        return nil, false
    }
    h, ok := p.Handlers[tool]
    return h, ok
}
```

**runtime.go** — change handler lookup in `processEffect`:

```go
// Before:
handler, ok := r.Effects.GetHandler(pe.Tool)

// After:
handler, ok := p.GetHandler(pe.Tool)
if !ok {
    handler, ok = r.Effects.GetHandler(pe.Tool)
}
```

Per-child handlers live exclusively on the Process. Never registered globally. The capability check already happens per-process. `processEffect` checks process handlers first, falls back to the global table.

**Tests:**

- Spawn process with handler override, verify it runs instead of global handler
- Spawn process without override, verify global handler still runs
- Override one tool, leave another global, verify correct dispatch

### Step 2: StepUntilDone on Runtime

Add a method that loops Step until the LLM returns no effects or the step limit is hit.

**runtime.go:**

```go
type StepCallback func(step int, text string, results []EffectResult) (inject string, stop bool)

func (r *Runtime) StepUntilDone(processID string, maxSteps int, onStep StepCallback) (string, TokenUsage, error) {
    var totalUsage TokenUsage
    var lastText string

    for step := 0; step < maxSteps; step++ {
        text, results, usage := r.Step(processID)
        totalUsage.InputTokens += usage.InputTokens
        totalUsage.OutputTokens += usage.OutputTokens
        lastText = text

        hasEffects := false
        for _, r := range results {
            if r.Admitted {
                hasEffects = true
                break
            }
        }

        if !hasEffects {
            break
        }

        if onStep == nil {
            continue
        }

        inject, stop := onStep(step, text, results)
        if inject != "" {
            r.Inject(processID, inject)
        }
        if stop {
            text, _, usage = r.Step(processID)
            totalUsage.InputTokens += usage.InputTokens
            totalUsage.OutputTokens += usage.OutputTokens
            lastText = text
            break
        }
    }

    return lastText, totalUsage, nil
}
```

**Tests:**

- Generator that returns effects for 3 steps then stops — verify 4 total
- maxSteps=2 with a generator that would run 5 — verify stops at 2
- StepCallback that injects a message — verify injection appears in log
- StepCallback that returns stop=true — verify one final step then done
- nil callback — verify runs to completion normally

### Step 3: New Fork signatures and Group

Replace `Fork([]string)` with variadic `Fork(prompts ...string)` and add `ForkAgents`.

**agent.go:**

```go
type Group struct {
    agent     *Agent
    children  []*Process
    rt        *Runtime
    parentID  string
    maxSteps  int
    onStep    StepCallback
}

func (a *Agent) Fork(prompts ...string) *Group {
    a.ensureRuntime()

    if a.proc == nil {
        a.proc = a.rt.Spawn(ProcessSpec{
            Name:         "coordinator",
            SystemPrompt: a.purpose,
        }, nil, "")
    }

    caps := make([]Capability, len(a.tools))
    for i, t := range a.tools {
        caps[i] = Capability(t.Name)
    }
    defs := a.toolDefs()

    var children []*Process
    for i, prompt := range prompts {
        child := a.rt.Fork(a.proc.ID, ProcessSpec{
            Name:         fmt.Sprintf("worker-%d", i),
            SystemPrompt: a.purpose,
        }, caps)
        child.Tools = defs
        child.View.Filter = func(e Event) bool {
            return e.ProcessID == child.ID
        }
        a.rt.Inject(child.ID, prompt)
        children = append(children, child)
    }

    return &Group{
        agent:    a,
        children: children,
        rt:       a.rt,
        parentID: a.proc.ID,
        maxSteps: 50,
    }
}

func ForkAgents(agents ...*Agent) *Group {
    // All agents must share a runtime for audit logging.
    // Use the first agent's runtime, or create a shared one.
    if len(agents) == 0 {
        return &Group{maxSteps: 50}
    }

    agents[0].ensureRuntime()
    rt := agents[0].rt

    coordinator := rt.Spawn(ProcessSpec{Name: "coordinator"}, nil, "")

    var children []*Process
    for i, a := range agents {
        a.ensureRuntime()

        caps := make([]Capability, len(a.tools))
        for j, t := range a.tools {
            caps[j] = Capability(t.Name)
        }

        child := rt.Fork(coordinator.ID, ProcessSpec{
            Name:         fmt.Sprintf("agent-%d", i),
            SystemPrompt: a.purpose,
        }, caps)

        child.Tools = a.toolDefs()
        child.Handlers = make(map[string]EffectHandler, len(a.tools))
        for _, t := range a.tools {
            child.Handlers[t.Name] = t.Handler
        }

        child.View.Filter = func(e Event) bool {
            return e.ProcessID == child.ID
        }

        // Transfer any pending task from the agent
        if a.proc != nil {
            // Agent already has a task injected via GiveTask — 
            // we need to inject it into the child process.
            // GiveTask puts an EventUserInput in the log for a.proc.ID.
            // For ForkAgents, we read a.proc's last input and re-inject.
            events := rt.Log.All()
            for j := len(events) - 1; j >= 0; j-- {
                if events[j].ProcessID == a.proc.ID && events[j].Type == EventUserInput {
                    rt.Inject(child.ID, events[j].Data["content"])
                    break
                }
            }
        }

        // Register handlers on the shared runtime's effect table
        // only if not already present (parent's handlers take precedence).
        // Actually — NO. Per-child handlers go on child.Handlers only.
        // The processEffect lookup (Step 1) handles dispatch.

        children = append(children, child)
    }

    return &Group{
        agent:    agents[0],
        children: children,
        rt:       rt,
        parentID: coordinator.ID,
        maxSteps: 50,
    }
}

// OnStep sets a callback that runs after each step for every child.
func (g *Group) OnStep(cb StepCallback) {
    g.onStep = cb
}

// SetMaxSteps sets the maximum number of steps each child can take.
// Default is 50.
func (g *Group) SetMaxSteps(n int) {
    g.maxSteps = n
}
```

**Tests:**

- Fork with variadic strings — children inherit parent's tools
- ForkAgents with differently-configured agents — each uses its own tools
- ForkAgents transfers GiveTask prompts to child processes

### Step 4: Result, Stream, and Join

**agent.go:**

```go
type Result struct {
    Index int
    Text  string
    Usage TokenUsage
    Err   error
}

func (g *Group) Stream() <-chan Result {
    ch := make(chan Result, len(g.children))
    var wg sync.WaitGroup
    wg.Add(len(g.children))

    for i, child := range g.children {
        go func() {
            defer wg.Done()
            defer func() {
                if r := recover(); r != nil {
                    ch <- Result{Index: i, Err: fmt.Errorf("panic: %v", r)}
                }
            }()

            text, usage, err := g.rt.StepUntilDone(child.ID, g.maxSteps, g.onStep)
            if g.agent != nil {
                g.agent.addUsage(usage)
            }
            g.rt.Join(g.parentID, child.ID, text)
            ch <- Result{Index: i, Text: text, Usage: usage, Err: err}
        }()
    }

    go func() {
        wg.Wait()
        close(ch)
    }()

    return ch
}

func (g *Group) Join() ([]Result, error) {
    results := make([]Result, len(g.children))
    var firstErr error

    for r := range g.Stream() {
        results[r.Index] = r
        if r.Err != nil && firstErr == nil {
            firstErr = r.Err
        }
    }

    return results, firstErr
}
```

**Tests:**

- Join collects all results in order by index
- Stream delivers results as they arrive
- Panicking child produces Result with Err, doesn't crash siblings
- Empty fork returns empty results
- Join propagates first error

### Step 5: Migrate triage tool

**processRepoInvestigations** becomes:

```go
func processRepoInvestigations(workspace string, gen *orchid.BedrockGenerator, batchSize int) error {
    // ... load repos and pending stubs (unchanged) ...

    for batchStart := 0; batchStart < len(repos); batchStart += batchSize {
        batch := repos[batchStart:min(batchStart+batchSize, len(repos))]

        var agents []*orchid.Agent
        var clonePaths []string
        for _, rw := range batch {
            clonePath, err := cloneRepo(rw.name)
            if err != nil {
                // handle clone failure
                continue
            }
            clonePaths = append(clonePaths, clonePath)

            a := orchid.NewFromGenerator(gen)
            a.Can(repoTools(clonePath)...)
            a.System("Security analyst. Be decisive.")
            a.GiveTask(buildInvestigationPrompt(rw.stubs))
            agents = append(agents, a)
        }
        defer func() {
            for _, p := range clonePaths {
                os.RemoveAll(p)
            }
        }()

        group := orchid.ForkAgents(agents...)
        group.SetMaxSteps(15)
        group.OnStep(budgetCallback(10))
        results, err := group.Join()
        // ... handle results, write triages ...
    }
    return nil
}
```

**investigateGroup** becomes:

```go
func investigateGroup(workspace string, reader *bufio.Reader, group VulnGroup, judgments []Judgment, gen *orchid.BedrockGenerator) ([]Judgment, error) {
    var agents []*orchid.Agent
    for _, repo := range group.Repos {
        clonePath, err := cloneRepo(repo)
        if err != nil {
            // handle
            continue
        }
        defer os.RemoveAll(clonePath)

        a := orchid.NewFromGenerator(gen)
        a.Can(repoTools(clonePath)...)
        a.System("Security analyst. Be decisive.")
        a.GiveTask(buildInvestigationPrompt(repo, group))
        agents = append(agents, a)
    }

    g := orchid.ForkAgents(agents...)
    g.SetMaxSteps(15)
    g.OnStep(budgetCallback(10))

    for r := range g.Stream() {
        fmt.Printf("\n--- %s [%d/%d] ---\n", group.Repos[r.Index], r.Index+1, len(agents))
        if r.Err != nil {
            fmt.Printf("  Investigation failed: %v\n", r.Err)
            continue
        }
        proposal, err := parseGroupProposal(r.Text)
        if err != nil {
            fmt.Printf("  Parse failed: %v\n", err)
            continue
        }
        fmt.Printf("  Classification: %s\n", proposal.classification)
        fmt.Print("  [a]ccept, [s]kip: ")
        input, _ := reader.ReadString('\n')
        if strings.TrimSpace(input) == "a" {
            // write triage
        }
    }
    // ... save judgments ...
}
```

**What gets deleted:**

- `sync.WaitGroup` in `processRepoInvestigations`
- `chan repoInvestigation` and goroutine plumbing in `investigateGroup`
- Manual panic recovery (handled by Stream)
- The `repoResult` and `repoInvestigation` types
- ~200 lines of concurrency boilerplate

## Migration Checklist

- [ ] Step 1: `Process.Handlers` + `GetHandler` + `processEffect` lookup change
- [ ] Step 1 tests
- [ ] Step 2: `StepCallback` type + `Runtime.StepUntilDone`
- [ ] Step 2 tests
- [ ] Step 3: `Fork(prompts ...string)` + `ForkAgents(agents ...*Agent)` + `Group` methods
- [ ] Step 3 tests
- [ ] Step 4: `Result` struct + `Group.Stream()` + `Group.Join()`
- [ ] Step 4 tests
- [ ] Step 5: Migrate triage tool `processRepoInvestigations`
- [ ] Step 5: Migrate triage tool `investigateGroup`
- [ ] Step 5: Delete dead concurrency types
- [ ] Update `cmd/api-demo` Fork calls to variadic
- [ ] Update `packgen` Fork calls to variadic
- [ ] Run all tests
