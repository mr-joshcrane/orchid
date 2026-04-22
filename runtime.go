package orchid

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
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
		Data: map[string]string{
			"name":          spec.Name,
			"parent":        parentID,
			"model":         spec.Model,
			"system_prompt": spec.SystemPrompt,
		},
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
func (r *Runtime) Join(parentID string, childID string, result string, usage TokenUsage) {
	r.Log.Append(Event{
		Type:      EventJoin,
		ProcessID: parentID,
		Data: map[string]string{
			"child_id":      childID,
			"result":        result,
			"input_tokens":  strconv.Itoa(usage.InputTokens),
			"output_tokens": strconv.Itoa(usage.OutputTokens),
		},
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
func (r *Runtime) Step(ctx context.Context, processID string) (string, []EffectResult, TokenUsage) {
	p := r.Processes.Get(processID)
	if p == nil {
		return "", nil, TokenUsage{}
	}
	p.State = ProcessRunning

	orchidCtx := BuildContext(p)
	orchidCtx.Ctx = ctx
	proposal := r.Generator.Generate(orchidCtx)

	p.History = proposal.History
	p.Usage.InputTokens += proposal.InputTokens
	p.Usage.OutputTokens += proposal.OutputTokens

	r.Log.Append(Event{
		Type:      EventAssistantOutput,
		ProcessID: processID,
		Data: map[string]string{
			"content":       proposal.Text,
			"prompt_id":     proposal.PromptID,
			"input_tokens":  strconv.Itoa(proposal.InputTokens),
			"output_tokens": strconv.Itoa(proposal.OutputTokens),
			"model":         p.Spec.Model,
		},
	})

	var results []EffectResult
	for _, pe := range proposal.Effects {
		result := r.processEffect(p, pe)
		results = append(results, result)
	}

	p.PendingResults = results

	p.State = ProcessWaiting
	return proposal.Text, results, TokenUsage{InputTokens: proposal.InputTokens, OutputTokens: proposal.OutputTokens}
}

type StepCallback func(step int, text string, results []EffectResult) (inject string, stop bool)

func (r *Runtime) StepUntilDone(ctx context.Context, processID string, maxSteps int, onStep StepCallback) (string, TokenUsage, error) {
	var totalUsage TokenUsage
	var lastText string

	for step := 0; step < maxSteps; step++ {
		text, results, usage := r.Step(ctx, processID)
		totalUsage.InputTokens += usage.InputTokens
		totalUsage.OutputTokens += usage.OutputTokens
		lastText = text

		hasEffects := false
		for _, er := range results {
			if er.Admitted {
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
			text, _, usage = r.Step(ctx, processID)
			totalUsage.InputTokens += usage.InputTokens
			totalUsage.OutputTokens += usage.OutputTokens
			lastText = text
			break
		}
	}

	return lastText, totalUsage, nil
}

type EffectResult struct {
	Tool      string
	ToolUseID string
	Admitted  bool
	Result    string
	Error     string
}

func (r *Runtime) processEffect(p *Process, pe ProposedEffect) EffectResult {
	cacheKey := effectCacheKey(pe)
	if p.EffectCache != nil {
		if cached, ok := p.EffectCache[cacheKey]; ok {
			cached.ToolUseID = pe.ToolUseID
			return cached
		}
	}

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
		return EffectResult{Tool: pe.Tool, ToolUseID: pe.ToolUseID, Admitted: false, Error: reason}
	}

	if ok, reason := r.Effects.CheckPolicies(p, eff); !ok {
		eff.State = EffectRejected
		r.Log.Append(Event{
			Type:      EventEffectRejected,
			ProcessID: p.ID,
			Data:      map[string]string{"effect_id": eff.ID, "reason": reason},
		})
		return EffectResult{Tool: pe.Tool, ToolUseID: pe.ToolUseID, Admitted: false, Error: reason}
	}

	eff.State = EffectAdmitted

	handler, ok := p.GetHandler(pe.Tool)
	if !ok {
		handler, ok = r.Effects.GetHandler(pe.Tool)
	}
	if !ok {
		eff.State = EffectRejected
		reason := fmt.Sprintf("no handler for tool: %s", pe.Tool)
		r.Log.Append(Event{
			Type:      EventEffectRejected,
			ProcessID: p.ID,
			Data:      map[string]string{"effect_id": eff.ID, "reason": reason},
		})
		return EffectResult{Tool: pe.Tool, ToolUseID: pe.ToolUseID, Admitted: true, Error: reason}
	}

	eff.State = EffectStarted

	start := time.Now()
	handlerResult, err := handler(pe.Args)
	duration := time.Since(start)

	if err != nil {
		eff.State = EffectRejected
		eff.Result = err.Error()
		r.Log.Append(Event{
			Type:      EventEffectRejected,
			ProcessID: p.ID,
			Data:      map[string]string{"effect_id": eff.ID, "reason": err.Error(), "duration_ms": strconv.FormatInt(duration.Milliseconds(), 10)},
		})
		return EffectResult{Tool: pe.Tool, ToolUseID: pe.ToolUseID, Admitted: true, Error: err.Error()}
	}

	eff.State = EffectObserved
	eff.Result = handlerResult
	r.Log.Append(Event{
		Type:      EventEffectObserved,
		ProcessID: p.ID,
		Data: map[string]string{
			"effect_id":   eff.ID,
			"tool":        pe.Tool,
			"result":      handlerResult,
			"duration_ms": strconv.FormatInt(duration.Milliseconds(), 10),
		},
	})

	eff.State = EffectApplied

	er := EffectResult{Tool: pe.Tool, ToolUseID: pe.ToolUseID, Admitted: true, Result: handlerResult}
	if p.EffectCache == nil {
		p.EffectCache = make(map[string]EffectResult)
	}
	p.EffectCache[cacheKey] = er
	return er
}

func effectCacheKey(pe ProposedEffect) string {
	keys := make([]string, 0, len(pe.Args))
	for k := range pe.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+pe.Args[k])
	}
	return pe.Tool + ":" + strings.Join(parts, "&")
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
