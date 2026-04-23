package orchid

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	gen     Generator
	tools   []Tool
	purpose string
	wf      *Workflow
	proc    *Process
	mu      sync.Mutex
	usage   TokenUsage
}

func New(gen Generator) *Agent {
	return &Agent{gen: gen}
}

func (w *Workflow) New(gen Generator) *Agent {
	return &Agent{gen: gen, wf: w}
}

func NewBedrockAgent(model string) (*Agent, error) {
	gen, err := NewBedrockGenerator(model)
	if err != nil {
		return nil, err
	}
	return New(gen), nil
}

func NewLocalAgent(endpoint, model string) *Agent {
	return New(NewOpenAIGenerator(endpoint, model))
}

func (a *Agent) Can(tools ...Tool) *Agent {
	a.tools = append(a.tools, tools...)
	return a
}

func (a *Agent) System(purpose string) *Agent {
	a.purpose = purpose
	return a
}

func (a *Agent) GiveTask(task string) {
	a.ensureWorkflow()

	if a.proc == nil {
		caps := make([]Capability, len(a.tools))
		for i, t := range a.tools {
			caps[i] = Capability(t.Name)
		}
		a.proc = a.wf.Spawn(ProcessSpec{
			Name:         "agent",
			SystemPrompt: a.purpose,
		}, caps, "")
		a.proc.Generator = a.gen
		a.proc.Tools = a.toolDefs()
	}

	a.wf.Inject(a.proc.ID, task)
}

func (a *Agent) addUsage(u TokenUsage) {
	a.mu.Lock()
	a.usage.InputTokens += u.InputTokens
	a.usage.OutputTokens += u.OutputTokens
	a.mu.Unlock()
}

func (a *Agent) Usage() TokenUsage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.usage
}

func (a *Agent) Step() (string, []EffectResult, error) {
	return a.StepWithContext(context.Background())
}

func (a *Agent) StepWithContext(ctx context.Context) (string, []EffectResult, error) {
	a.ensureWorkflow()
	if a.proc == nil {
		return "", nil, fmt.Errorf("no active process: call GiveTask first")
	}
	text, results, usage := a.wf.Step(ctx, a.proc.ID)
	a.addUsage(usage)
	if a.proc.Err != nil {
		return "", nil, a.proc.Err
	}
	return text, results, nil
}

func (a *Agent) Fork(prompts ...string) *Group {
	a.ensureWorkflow()

	if a.proc == nil {
		a.proc = a.wf.Spawn(ProcessSpec{
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
		child := a.wf.Fork(a.proc.ID, ProcessSpec{
			Name:         fmt.Sprintf("worker-%d", i),
			SystemPrompt: a.purpose,
		}, caps)

		child.Generator = a.gen
		child.Tools = defs
		child.View.Filter = func(e Event) bool {
			return e.ProcessID == child.ID
		}

		a.wf.Inject(child.ID, prompt)
		children = append(children, child)
	}

	return &Group{
		agent:    a,
		children: children,
		wf:       a.wf,
		parentID: a.proc.ID,
		maxSteps: 50,
	}
}

func ForkAgents(agents ...*Agent) *Group {
	if len(agents) == 0 {
		return &Group{maxSteps: 50}
	}

	agents[0].ensureWorkflow()
	wf := agents[0].wf

	coordinator := wf.Spawn(ProcessSpec{Name: "coordinator"}, nil, "")

	var children []*Process
	for i, a := range agents {
		caps := make([]Capability, len(a.tools))
		for j, t := range a.tools {
			caps[j] = Capability(t.Name)
		}

		child := wf.Fork(coordinator.ID, ProcessSpec{
			Name:         fmt.Sprintf("agent-%d", i),
			SystemPrompt: a.purpose,
		}, caps)

		child.Generator = a.gen
		child.Tools = a.toolDefs()
		child.Handlers = make(map[string]EffectHandler, len(a.tools))
		for _, t := range a.tools {
			child.Handlers[t.Name] = t.Handler
		}

		child.View.Filter = func(e Event) bool {
			return e.ProcessID == child.ID
		}

		if a.proc != nil {
			events := a.wf.Log.All()
			for j := len(events) - 1; j >= 0; j-- {
				if events[j].ProcessID == a.proc.ID && events[j].Type == EventUserInput {
					wf.Inject(child.ID, events[j].Data["content"])
					break
				}
			}
		}

		children = append(children, child)
	}

	return &Group{
		agent:    agents[0],
		children: children,
		wf:       wf,
		parentID: coordinator.ID,
		maxSteps: 50,
	}
}

func (a *Agent) ensureWorkflow() {
	if a.wf != nil {
		return
	}
	a.wf = &Workflow{
		Log:       NewEventLog(),
		Processes: NewProcessTable(),
		Effects:   NewEffectTable(),
	}
	for _, t := range a.tools {
		a.wf.Effects.RegisterHandler(t.Name, t.Handler)
	}
}

func (a *Agent) toolDefs() []ToolDefinition {
	defs := make([]ToolDefinition, len(a.tools))
	for i, t := range a.tools {
		defs[i] = ToolDefinition{Name: t.Name, Description: t.Description}
	}
	return defs
}

type Result struct {
	Index int
	Text  string
	Usage TokenUsage
	Err   error
}

type Group struct {
	agent    *Agent
	children []*Process
	wf       *Workflow
	parentID string
	maxSteps int
	onStep   StepCallback
}

func (g *Group) OnStep(cb StepCallback) {
	g.onStep = cb
}

func (g *Group) SetMaxSteps(n int) {
	g.maxSteps = n
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

			text, usage, err := g.wf.StepUntilDone(context.Background(), child.ID, g.maxSteps, g.onStep)
			if g.agent != nil {
				g.agent.addUsage(usage)
			}
			g.wf.Join(g.parentID, child.ID, text, usage)
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

// Log returns all events from this agent's runtime.
func (a *Agent) Log() []Event {
	if a.wf == nil {
		return nil
	}
	return a.wf.Log.All()
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
		pid := e.Data["prompt_id"]
		content := truncateStr(e.Data["content"], 80)
		if pid != "" {
			return fmt.Sprintf("[%s] %s", pid, content)
		}
		return content
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

type ProcessNode struct {
	ID          string
	Name        string
	Model       string
	OwnUsage    TokenUsage
	ChildUsage  TokenUsage
	TotalUsage  TokenUsage
	Children    []*ProcessNode
	StepCount   int
	EffectCount int
}

func BuildProcessTree(events []Event) []*ProcessNode {
	nodes := map[string]*ProcessNode{}
	parentOf := map[string]string{}
	childOrder := map[string][]string{}

	for _, e := range events {
		switch e.Type {
		case EventProcessSpawned:
			nodes[e.ProcessID] = &ProcessNode{
				ID:    e.ProcessID,
				Name:  e.Data["name"],
				Model: e.Data["model"],
			}
			parent := e.Data["parent"]
			if parent != "" {
				parentOf[e.ProcessID] = parent
			}

		case EventFork:
			childID := e.Data["child_id"]
			childOrder[e.ProcessID] = append(childOrder[e.ProcessID], childID)

		case EventAssistantOutput:
			node, ok := nodes[e.ProcessID]
			if !ok {
				continue
			}
			node.StepCount++
			inTok, _ := strconv.Atoi(e.Data["input_tokens"])
			outTok, _ := strconv.Atoi(e.Data["output_tokens"])
			node.OwnUsage.InputTokens += inTok
			node.OwnUsage.OutputTokens += outTok

		case EventEffectObserved:
			node, ok := nodes[e.ProcessID]
			if !ok {
				continue
			}
			node.EffectCount++
		}
	}

	for parentID, order := range childOrder {
		parent, ok := nodes[parentID]
		if !ok {
			continue
		}
		for _, childID := range order {
			child, ok := nodes[childID]
			if !ok {
				continue
			}
			parent.Children = append(parent.Children, child)
		}
	}

	var roots []*ProcessNode
	for id, node := range nodes {
		if _, hasParent := parentOf[id]; !hasParent {
			roots = append(roots, node)
		}
	}

	sort.Slice(roots, func(i, j int) bool {
		return roots[i].ID < roots[j].ID
	})

	for _, root := range roots {
		rollupUsage(root)
	}

	return roots
}

func rollupUsage(node *ProcessNode) {
	node.ChildUsage = TokenUsage{}
	for _, child := range node.Children {
		rollupUsage(child)
		node.ChildUsage.InputTokens += child.TotalUsage.InputTokens
		node.ChildUsage.OutputTokens += child.TotalUsage.OutputTokens
	}
	node.TotalUsage = TokenUsage{
		InputTokens:  node.OwnUsage.InputTokens + node.ChildUsage.InputTokens,
		OutputTokens: node.OwnUsage.OutputTokens + node.ChildUsage.OutputTokens,
	}
}

func PrintTree(w io.Writer, roots []*ProcessNode) {
	for _, root := range roots {
		printNode(w, root, "", true)
	}
}

func printNode(w io.Writer, node *ProcessNode, prefix string, isLast bool) {
	connector := "├── "
	if isLast {
		connector = "└── "
	}
	if prefix == "" {
		connector = ""
	}

	model := ""
	if node.Model != "" {
		model = fmt.Sprintf("  model=%s", node.Model)
	}

	detail := ""
	if node.StepCount > 0 || node.EffectCount > 0 {
		detail = fmt.Sprintf("  [%d steps, %d effects]", node.StepCount, node.EffectCount)
	}

	fmt.Fprintf(w, "%s%s%s (%s)%s  tokens=%d/%d  total=%d/%d%s\n",
		prefix, connector,
		node.Name, node.ID, model,
		node.OwnUsage.InputTokens, node.OwnUsage.OutputTokens,
		node.TotalUsage.InputTokens, node.TotalUsage.OutputTokens,
		detail)

	childPrefix := prefix
	if prefix != "" {
		if isLast {
			childPrefix += "    "
		} else {
			childPrefix += "│   "
		}
	}

	for i, child := range node.Children {
		printNode(w, child, childPrefix, i == len(node.Children)-1)
	}
}

func File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func Folder(root string, includeFilter ...string) (string, error) {
	var contents strings.Builder
	var walkErr error
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			walkErr = err
			return nil
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if len(includeFilter) > 0 {
			matched := false
			for _, f := range includeFilter {
				if strings.Contains(info.Name(), f) {
					matched = true
					break
				}
			}
			if !matched {
				return nil
			}
		}
		data, err := File(path)
		if err != nil {
			walkErr = err
			return nil
		}
		contents.WriteString(data)
		contents.WriteByte('\n')
		return nil
	})
	return contents.String(), walkErr
}
