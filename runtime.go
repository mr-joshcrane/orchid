package orchid

import (
	"fmt"
	"sync/atomic"
)

// Runtime is the orchestration kernel.
type Runtime struct {
	Log       *EventLog
	Processes *ProcessTable
	Effects   *EffectTable
	Generator Generator
	nextPID   atomic.Uint64
}

func NewRuntime(gen Generator) *Runtime {
	return &Runtime{
		Log:       NewEventLog(),
		Processes: NewProcessTable(),
		Effects:   NewEffectTable(),
		Generator: gen,
	}
}

func (r *Runtime) newProcessID() string {
	return fmt.Sprintf("proc_%d", r.nextPID.Add(1))
}

func (r *Runtime) newEffectID() string {
	return fmt.Sprintf("eff_%d", r.nextPID.Add(1))
}

// Spawn creates a new process and registers it.
func (r *Runtime) Spawn(spec ProcessSpec, caps []Capability, parentID string) *Process {
	p := &Process{
		ID:           r.newProcessID(),
		Spec:         spec,
		Capabilities: make(map[Capability]bool),
		View: LogView{
			Source:   r.Log,
			StartCut: uint64(r.Log.Len()),
		},
		State:    ProcessReady,
		ParentID: parentID,
	}
	for _, c := range caps {
		p.Capabilities[c] = true
	}

	r.Processes.Add(p)
	r.Log.Append(Event{
		Type:      EventProcessSpawned,
		ProcessID: p.ID,
		Data:      map[string]string{"name": spec.Name, "parent": parentID},
	})
	return p
}

// Fork creates a child process that sees the parent's log from the fork point.
func (r *Runtime) Fork(parentID string, childSpec ProcessSpec, caps []Capability) *Process {
	child := r.Spawn(childSpec, caps, parentID)
	child.View.StartCut = 0
	child.View.Source = r.Log

	r.Log.Append(Event{
		Type:      EventFork,
		ProcessID: parentID,
		Data:      map[string]string{"child_id": child.ID},
	})
	return child
}

// Join imports a child process's result into the parent's log.
func (r *Runtime) Join(parentID string, childID string, result string) {
	r.Log.Append(Event{
		Type:      EventJoin,
		ProcessID: parentID,
		Data:      map[string]string{"child_id": childID, "result": result},
	})

	child := r.Processes.Get(childID)
	if child != nil {
		child.State = ProcessDone
		r.Log.Append(Event{
			Type:      EventProcessExited,
			ProcessID: childID,
		})
	}
}

// Inject adds a user input event to the log for a process.
func (r *Runtime) Inject(processID string, input string) {
	r.Log.Append(Event{
		Type:      EventUserInput,
		ProcessID: processID,
		Data:      map[string]string{"content": input},
	})
}

// Send delivers a message from one process to another.
func (r *Runtime) Send(from, to, content string) {
	r.Log.Append(Event{
		Type:      EventMessage,
		ProcessID: to,
		Data:      map[string]string{"from": from, "content": content},
	})
}

// Step runs one generate-then-execute cycle for a process.
// Returns the proposal text and any effect results.
func (r *Runtime) Step(processID string) (string, []EffectResult) {
	p := r.Processes.Get(processID)
	if p == nil {
		return "", nil
	}
	p.State = ProcessRunning

	ctx := BuildContext(p)
	proposal := r.Generator.Generate(ctx)

	r.Log.Append(Event{
		Type:      EventAssistantOutput,
		ProcessID: processID,
		Data:      map[string]string{"content": proposal.Text},
	})

	var results []EffectResult
	for _, pe := range proposal.Effects {
		result := r.processEffect(p, pe)
		results = append(results, result)
	}

	p.State = ProcessWaiting
	return proposal.Text, results
}

// EffectResult captures the outcome of processing one proposed effect.
type EffectResult struct {
	Tool     string
	Admitted bool
	Result   string
	Error    string
}

func (r *Runtime) processEffect(p *Process, pe ProposedEffect) EffectResult {
	eff := &Effect{
		ID:        r.newEffectID(),
		ProcessID: p.ID,
		Tool:      pe.Tool,
		Args:      pe.Args,
		State:     EffectPlanned,
	}

	intentEventID := r.Log.Append(Event{
		Type:      EventEffectIntent,
		ProcessID: p.ID,
		Data:      mergeMap(map[string]string{"tool": pe.Tool, "effect_id": eff.ID}, pe.Args),
	})
	eff.EventID = intentEventID
	r.Effects.Track(eff)

	if !p.HasCapability(Capability(pe.Tool)) {
		eff.State = EffectRejected
		reason := fmt.Sprintf("process lacks capability: %s", pe.Tool)
		r.Log.Append(Event{
			Type:      EventEffectRejected,
			ProcessID: p.ID,
			Data:      map[string]string{"effect_id": eff.ID, "reason": reason},
		})
		return EffectResult{Tool: pe.Tool, Admitted: false, Error: reason}
	}

	if ok, reason := r.Effects.CheckPolicies(p, eff); !ok {
		eff.State = EffectRejected
		r.Log.Append(Event{
			Type:      EventEffectRejected,
			ProcessID: p.ID,
			Data:      map[string]string{"effect_id": eff.ID, "reason": reason},
		})
		return EffectResult{Tool: pe.Tool, Admitted: false, Error: reason}
	}

	eff.State = EffectAdmitted
	r.Log.Append(Event{
		Type:      EventEffectAdmitted,
		ProcessID: p.ID,
		Data:      map[string]string{"effect_id": eff.ID, "tool": pe.Tool},
	})

	handler, ok := r.Effects.GetHandler(pe.Tool)
	if !ok {
		eff.State = EffectRejected
		reason := fmt.Sprintf("no handler for tool: %s", pe.Tool)
		r.Log.Append(Event{
			Type:      EventEffectRejected,
			ProcessID: p.ID,
			Data:      map[string]string{"effect_id": eff.ID, "reason": reason},
		})
		return EffectResult{Tool: pe.Tool, Admitted: true, Error: reason}
	}

	eff.State = EffectStarted
	r.Log.Append(Event{
		Type:      EventEffectStarted,
		ProcessID: p.ID,
		Data:      map[string]string{"effect_id": eff.ID, "tool": pe.Tool},
	})

	result, err := handler(pe.Args)
	if err != nil {
		eff.State = EffectRejected
		eff.Result = err.Error()
		r.Log.Append(Event{
			Type:      EventEffectRejected,
			ProcessID: p.ID,
			Data:      map[string]string{"effect_id": eff.ID, "reason": err.Error()},
		})
		return EffectResult{Tool: pe.Tool, Admitted: true, Error: err.Error()}
	}

	eff.State = EffectObserved
	eff.Result = result
	r.Log.Append(Event{
		Type:      EventEffectObserved,
		ProcessID: p.ID,
		Data:      map[string]string{"effect_id": eff.ID, "tool": pe.Tool, "result": result},
	})

	eff.State = EffectApplied
	r.Log.Append(Event{
		Type:      EventEffectApplied,
		ProcessID: p.ID,
		Data:      map[string]string{"effect_id": eff.ID, "tool": pe.Tool},
	})

	return EffectResult{Tool: pe.Tool, Admitted: true, Result: result}
}

func mergeMap(base, overlay map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}
