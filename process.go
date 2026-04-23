package orchid

import "sync"

// Capability represents what a process is allowed to do.
type Capability string

const (
	CapRead  Capability = "read"
	CapWrite Capability = "write"
	CapEdit  Capability = "edit"
	CapBash  Capability = "bash"
	CapNet   Capability = "net"
)

// LogView defines what portion of the log a process can see.
type LogView struct {
	Source   *EventLog
	StartCut uint64
	Filter   func(Event) bool
}

// ProcessSpec defines the identity of a process — its prompt, model, etc.
type ProcessSpec struct {
	Name         string
	SystemPrompt string
	Model        string
	Temperature  float64
}

// ProcessState represents the lifecycle of a process.
type ProcessState int

const (
	ProcessReady ProcessState = iota
	ProcessRunning
	ProcessWaiting
	ProcessDone
	ProcessFailed
)

// Process is a running agent in the runtime.
type Process struct {
	ID             string
	Spec           ProcessSpec
	Generator      Generator
	Capabilities   map[Capability]bool
	View           LogView
	State          ProcessState
	Err            error
	ParentID       string
	Mailbox        []Event
	Tools          []ToolDefinition
	Handlers       map[string]EffectHandler
	History        any
	PendingResults []EffectResult
	EffectCache    map[string]EffectResult
	Usage          TokenUsage
}

func (p *Process) HasCapability(c Capability) bool {
	return p.Capabilities[c]
}

// GetHandler returns a process-local handler override for the given tool.
func (p *Process) GetHandler(tool string) (EffectHandler, bool) {
	if p.Handlers == nil {
		return nil, false
	}
	h, ok := p.Handlers[tool]
	return h, ok
}

// ProcessTable manages all active processes.
type ProcessTable struct {
	mu        sync.RWMutex
	processes map[string]*Process
}

func NewProcessTable() *ProcessTable {
	return &ProcessTable{
		processes: make(map[string]*Process),
	}
}

func (pt *ProcessTable) Add(p *Process) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.processes[p.ID] = p
}

func (pt *ProcessTable) Get(id string) *Process {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return pt.processes[id]
}

func (pt *ProcessTable) Remove(id string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	delete(pt.processes, id)
}

func (pt *ProcessTable) All() []*Process {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	out := make([]*Process, 0, len(pt.processes))
	for _, p := range pt.processes {
		out = append(out, p)
	}
	return out
}
