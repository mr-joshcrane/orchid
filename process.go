package orchid

import "sync"

// Capability is a tool name granted to a process. Grants live in Permissions.
type Capability = ToolName

type LogView struct {
	Source   *EventLog
	StartCut uint64
	Filter   func(Event) bool
}

type ProcessState int

const (
	ProcessReady ProcessState = iota
	ProcessRunning
	ProcessWaiting
	ProcessFailed
)

type Process struct {
	ID            string
	Name          string
	Purpose       string
	Generator     Generator
	Permissions   map[Permission]bool
	View          LogView
	State         ProcessState
	Err           error
	ParentID      string
	Tools         []ToolDefinition
	Usage         TokenUsage
	ContextPolicy ContextPolicy
}

func (p *Process) HasPermission(perm Permission) bool {
	return p.Permissions[perm]
}

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

func (pt *ProcessTable) All() []*Process {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	out := make([]*Process, 0, len(pt.processes))
	for _, p := range pt.processes {
		out = append(out, p)
	}
	return out
}
