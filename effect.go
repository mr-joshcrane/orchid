package orchid

import (
	"fmt"
	"sync"
)

// EffectState tracks where an effect is in its lifecycle.
type EffectState int

const (
	EffectPlanned EffectState = iota
	EffectAdmitted
	EffectStarted
	EffectObserved
	EffectApplied
	EffectRejected
)

func (s EffectState) String() string {
	names := []string{"Planned", "Admitted", "Started", "Observed", "Applied", "Rejected"}
	if int(s) < len(names) {
		return names[s]
	}
	return fmt.Sprintf("EffectState(%d)", s)
}

// Effect represents a proposed or in-flight side effect.
type Effect struct {
	ID        string
	ProcessID string
	Tool      string
	Args      map[string]string
	State     EffectState
	Result    string
	EventID   uint64
}

// EffectHandler executes a specific tool type and returns the result.
type EffectHandler func(args map[string]string) (string, error)

// EffectPolicy decides whether an effect should be admitted.
type EffectPolicy func(process *Process, effect *Effect) (bool, string)

// EffectTable manages the lifecycle of all effects.
type EffectTable struct {
	mu       sync.RWMutex
	effects  map[string]*Effect
	handlers map[string]EffectHandler
	policies []EffectPolicy
}

func NewEffectTable() *EffectTable {
	return &EffectTable{
		effects:  make(map[string]*Effect),
		handlers: make(map[string]EffectHandler),
	}
}

func (et *EffectTable) RegisterHandler(tool string, handler EffectHandler) {
	et.mu.Lock()
	defer et.mu.Unlock()
	et.handlers[tool] = handler
}

func (et *EffectTable) AddPolicy(policy EffectPolicy) {
	et.mu.Lock()
	defer et.mu.Unlock()
	et.policies = append(et.policies, policy)
}

func (et *EffectTable) Track(e *Effect) {
	et.mu.Lock()
	defer et.mu.Unlock()
	et.effects[e.ID] = e
}

func (et *EffectTable) Get(id string) *Effect {
	et.mu.RLock()
	defer et.mu.RUnlock()
	return et.effects[id]
}

func (et *EffectTable) CheckPolicies(process *Process, effect *Effect) (bool, string) {
	et.mu.RLock()
	defer et.mu.RUnlock()
	for _, policy := range et.policies {
		if ok, reason := policy(process, effect); !ok {
			return false, reason
		}
	}
	return true, ""
}

func (et *EffectTable) GetHandler(tool string) (EffectHandler, bool) {
	et.mu.RLock()
	defer et.mu.RUnlock()
	h, ok := et.handlers[tool]
	return h, ok
}
