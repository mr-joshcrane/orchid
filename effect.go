package orchid

import (
	"context"
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
	// ToolUseID pairs this effect with the model's tool call. It falls back to
	// ID when the generator proposed no id of its own.
	ToolUseID string
}

// EffectHandler executes a specific tool type and returns the result.
type EffectHandler func(args map[string]string) (string, error)

type HandlerContext struct {
	Ctx       context.Context
	ProcessID string
	ToolName  string
	RootDir   string // populated in Phase 3 via WithRootDir; empty until then
	LogAppend func(Event)
	// EffectID and ToolUseID identify the call being handled, so an event a
	// handler appends itself correlates with the effect's own audit records.
	EffectID  string
	ToolUseID string
}

type ContextualEffectHandler func(hctx HandlerContext, args map[string]string) (string, error)

type Result struct {
	Content  string
	MIME     string
	Bytes    int
	Metadata map[string]string
}

type RichEffectHandler func(hctx HandlerContext, args map[string]string) (Result, error)

// EffectPolicy decides whether an effect should be admitted.
type EffectPolicy func(process *Process, effect *Effect) (bool, string)

// EffectTable manages the lifecycle of all effects.
//
// Handlers have exactly one registration path, RegisterTool, which normalises
// every supported handler shape to a ContextualEffectHandler. A second, parallel
// map keyed by tool name would admit a nil handler for a tool that set one of
// the other shapes, and dispatch would then call it.
type EffectTable struct {
	mu                 sync.RWMutex
	effects            map[string]*Effect
	contextualHandlers map[string]ContextualEffectHandler
	policies           []EffectPolicy
}

func NewEffectTable() *EffectTable {
	return &EffectTable{
		effects:            make(map[string]*Effect),
		contextualHandlers: make(map[string]ContextualEffectHandler),
	}
}

// RegisterTool normalises a tool's handler and registers it under the tool name.
// It reports an error unless exactly one handler shape is set.
func (et *EffectTable) RegisterTool(tool Tool) error {
	handlerCount := 0
	if tool.Handler != nil {
		handlerCount++
	}
	if tool.ContextualHandler != nil {
		handlerCount++
	}
	if tool.RichHandler != nil {
		handlerCount++
	}
	if handlerCount > 1 {
		return fmt.Errorf("orchid: tool %q has multiple handlers set; exactly one must be non-nil", tool.Name)
	}
	if handlerCount == 0 {
		return fmt.Errorf("orchid: tool %q has no handler set; exactly one must be non-nil", tool.Name)
	}
	et.mu.Lock()
	defer et.mu.Unlock()
	var handler ContextualEffectHandler
	switch {
	case tool.RichHandler != nil:
		handler = adaptRichHandler(tool.RichHandler)
	case tool.ContextualHandler != nil:
		handler = tool.ContextualHandler
	default:
		handler = adaptPlainHandler(tool.Handler)
	}
	et.contextualHandlers[tool.Name] = handler
	return nil
}

func (et *EffectTable) GetContextualHandler(tool string) (ContextualEffectHandler, bool) {
	et.mu.RLock()
	defer et.mu.RUnlock()
	h, ok := et.contextualHandlers[tool]
	return h, ok
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
