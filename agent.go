package orchid

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

type Tool struct {
	Name        string
	Description string
	Handler     EffectHandler
}

type Agent struct {
	endpoint string
	model    string
	gen      Generator
	tools    []Tool
	purpose  string
	rt       *Runtime
	proc     *Process
}

func New(endpoint, model string) *Agent {
	return &Agent{
		endpoint: endpoint,
		model:    model,
	}
}

func NewFromGenerator(gen Generator) *Agent {
	return &Agent{gen: gen}
}

func (a *Agent) Can(tools ...Tool) *Agent {
	a.tools = append(a.tools, tools...)
	return a
}

func (a *Agent) System(purpose string) *Agent {
	a.purpose = purpose
	return a
}

func (a *Agent) Run(task string) (string, []EffectResult, error) {
	a.ensureRuntime()

	if a.proc == nil {
		caps := make([]Capability, len(a.tools))
		for i, t := range a.tools {
			caps[i] = Capability(t.Name)
		}
		a.proc = a.rt.Spawn(ProcessSpec{
			Name:         "agent",
			SystemPrompt: a.purpose,
		}, caps, "")
		a.proc.Tools = a.toolDefs()
	}

	a.rt.Inject(a.proc.ID, task)
	text, results := a.rt.Step(a.proc.ID)
	return text, results, nil
}

func (a *Agent) Fork(tasks []string) *Group {
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
	for i, task := range tasks {
		child := a.rt.Fork(a.proc.ID, ProcessSpec{
			Name:         fmt.Sprintf("worker-%d", i),
			SystemPrompt: a.purpose,
		}, caps)

		child.Tools = defs
		child.View.Filter = func(e Event) bool {
			return e.ProcessID == child.ID
		}

		a.rt.Inject(child.ID, task)
		children = append(children, child)
	}

	return &Group{
		agent:    a,
		children: children,
	}
}

func (a *Agent) Rt() *Runtime {
	a.ensureRuntime()
	return a.rt
}

func (a *Agent) ensureRuntime() {
	if a.rt != nil {
		return
	}
	gen := a.gen
	if gen == nil {
		gen = &llmGenerator{
			endpoint: a.endpoint,
			model:    a.model,
		}
	}
	a.rt = NewRuntime(gen)
	for _, t := range a.tools {
		a.rt.Effects.RegisterHandler(t.Name, t.Handler)
	}
}

func (a *Agent) toolDefs() []ToolDefinition {
	defs := make([]ToolDefinition, len(a.tools))
	for i, t := range a.tools {
		defs[i] = ToolDefinition{Name: t.Name, Description: t.Description}
	}
	return defs
}

type Group struct {
	agent    *Agent
	children []*Process
}

func (g *Group) Join() ([]string, error) {
	type childResult struct {
		index int
		text  string
	}

	collected := make([]childResult, len(g.children))
	var wg sync.WaitGroup
	wg.Add(len(g.children))

	for i, child := range g.children {
		go func() {
			defer wg.Done()
			text, _ := g.agent.rt.Step(child.ID)
			collected[i] = childResult{index: i, text: text}
		}()
	}
	wg.Wait()

	results := make([]string, len(g.children))
	for _, cr := range collected {
		g.agent.rt.Join(g.agent.proc.ID, g.children[cr.index].ID, cr.text)
		results[cr.index] = cr.text
	}
	return results, nil
}

// Log returns all events from this agent's runtime.
func (a *Agent) Log() []Event {
	if a.rt == nil {
		return nil
	}
	return a.rt.Log.All()
}

// PrintLog writes a human-readable audit trail of events.
func PrintLog(w io.Writer, events []Event) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "ID\tTIME\tPROCESS\tEVENT\tDETAILS\n")
	fmt.Fprintf(tw, "--\t----\t-------\t-----\t-------\n")

	var t0 time.Time
	for _, e := range events {
		if t0.IsZero() {
			t0 = e.Timestamp
		}
		elapsed := e.Timestamp.Sub(t0)

		details := formatEventData(e)
		fmt.Fprintf(tw, "%d\t+%s\t%s\t%s\t%s\n",
			e.ID, elapsed.Truncate(time.Millisecond), e.ProcessID, e.Type, details)
	}
	tw.Flush()
}

func formatEventData(e Event) string {
	switch e.Type {
	case EventUserInput:
		return truncateStr(e.Data["content"], 80)
	case EventAssistantOutput:
		return truncateStr(e.Data["content"], 80)
	case EventEffectIntent:
		return fmt.Sprintf("tool=%s %s", e.Data["tool"], truncateArgs(e.Data, "tool", "effect_id"))
	case EventEffectRejected:
		return fmt.Sprintf("reason=%q", e.Data["reason"])
	case EventEffectObserved:
		return fmt.Sprintf("tool=%s result=%s", e.Data["tool"], truncateStr(e.Data["result"], 60))
	case EventProcessSpawned:
		if e.Data["parent"] != "" {
			return fmt.Sprintf("name=%s parent=%s", e.Data["name"], e.Data["parent"])
		}
		return fmt.Sprintf("name=%s", e.Data["name"])
	case EventFork:
		return fmt.Sprintf("child=%s", e.Data["child_id"])
	case EventJoin:
		return fmt.Sprintf("child=%s result=%s", e.Data["child_id"], truncateStr(e.Data["result"], 60))
	default:
		id, hasID := e.Data["effect_id"]
		tool := e.Data["tool"]
		if hasID && tool != "" {
			return fmt.Sprintf("effect=%s tool=%s", id, tool)
		}
		if hasID {
			return fmt.Sprintf("effect=%s", id)
		}
		return ""
	}
}

func truncateStr(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func truncateArgs(data map[string]string, skip ...string) string {
	skipSet := make(map[string]bool, len(skip))
	for _, s := range skip {
		skipSet[s] = true
	}
	var parts []string
	for k, v := range data {
		if skipSet[k] {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, truncateStr(v, 40)))
	}
	return strings.Join(parts, " ")
}

func (a *Agent) lastAssistantText() string {
	if a.proc == nil {
		return ""
	}
	var last string
	for _, e := range a.rt.Log.All() {
		if e.Type == EventAssistantOutput && e.ProcessID == a.proc.ID {
			if content := e.Data["content"]; strings.TrimSpace(content) != "" {
				last = content
			}
		}
	}
	return last
}
