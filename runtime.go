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

type Workflow struct {
	Log       *EventLog
	Processes *ProcessTable
	Effects   *EffectTable
	gen       Generator
	tools     []Tool
	idPrefix  string
	nextPID   atomic.Uint64
}

func NewWorkflow(gen Generator, tools ...Tool) *Workflow {
	wf := &Workflow{
		Log:       NewEventLog(),
		Processes: NewProcessTable(),
		Effects:   NewEffectTable(),
		gen:       gen,
		tools:     tools,
	}
	for _, t := range tools {
		wf.Effects.RegisterHandler(t.Name, t.Handler)
	}
	return wf
}

func (w *Workflow) WithIDPrefix(prefix string) *Workflow {
	w.idPrefix = prefix
	return w
}

func (w *Workflow) newProcessID() string {
	id := fmt.Sprintf("proc_%d", w.nextPID.Add(1))
	if w.idPrefix != "" {
		return w.idPrefix + "_" + id
	}
	return id
}

func (w *Workflow) newEffectID() string {
	id := fmt.Sprintf("eff_%d", w.nextPID.Add(1))
	if w.idPrefix != "" {
		return w.idPrefix + "_" + id
	}
	return id
}

func (w *Workflow) Spawn(name, purpose string, caps []Capability, parentID string) *Process {
	p := &Process{
		ID:           w.newProcessID(),
		Name:         name,
		Purpose:      purpose,
		Capabilities: make(map[Capability]bool),
		View: LogView{
			Source:   w.Log,
			StartCut: uint64(w.Log.Len()),
		},
		State:    ProcessReady,
		ParentID: parentID,
	}
	for _, c := range caps {
		p.Capabilities[c] = true
	}

	w.Processes.Add(p)
	w.Log.Append(Event{
		Type:      EventProcessSpawned,
		ProcessID: p.ID,
		Data: map[string]string{
			"name":    name,
			"purpose": purpose,
			"parent":  parentID,
		},
	})
	return p
}

func (w *Workflow) Inject(processID string, input string) {
	w.Log.Append(Event{
		Type:      EventUserInput,
		ProcessID: processID,
		Data:      map[string]string{"content": input},
	})
}

func (w *Workflow) Step(ctx context.Context, processID string) (string, []EffectResult, TokenUsage) {
	p := w.Processes.Get(processID)
	if p == nil {
		return "", nil, TokenUsage{}
	}
	p.State = ProcessRunning

	orchidCtx := BuildContext(p)
	if ctx == nil {
		ctx = context.Background()
	}
	orchidCtx.Ctx = ctx

	gen := p.Generator
	if gen == nil {
		gen = w.gen
	}
	proposal := gen.Generate(orchidCtx)

	if proposal.Err != nil {
		p.State = ProcessFailed
		p.Err = proposal.Err
		return "", nil, TokenUsage{}
	}

	p.History = proposal.History
	p.Usage.InputTokens += proposal.InputTokens
	p.Usage.OutputTokens += proposal.OutputTokens

	model := ""
	if mi, ok := gen.(ModelIdentifier); ok {
		model = mi.Model()
	}

	w.Log.Append(Event{
		Type:      EventAssistantOutput,
		ProcessID: processID,
		Data: map[string]string{
			"content":       proposal.Text,
			"prompt_id":     proposal.PromptID,
			"input_tokens":  strconv.Itoa(proposal.InputTokens),
			"output_tokens": strconv.Itoa(proposal.OutputTokens),
			"model":         model,
		},
	})

	var results []EffectResult
	for _, pe := range proposal.Effects {
		result := w.processEffect(p, pe)
		results = append(results, result)
	}

	p.PendingResults = results

	p.State = ProcessWaiting
	return proposal.Text, results, TokenUsage{InputTokens: proposal.InputTokens, OutputTokens: proposal.OutputTokens}
}

type StepCallback func(step int, text string, results []EffectResult) (inject string, stop bool)

func (w *Workflow) StepUntilDone(ctx context.Context, processID string, maxSteps int, onStep StepCallback) (string, TokenUsage, error) {
	var totalUsage TokenUsage
	var lastText string

	for step := 0; step < maxSteps; step++ {
		text, results, usage := w.Step(ctx, processID)
		totalUsage.InputTokens += usage.InputTokens
		totalUsage.OutputTokens += usage.OutputTokens
		lastText = text

		p := w.Processes.Get(processID)
		if p != nil && p.Err != nil {
			return "", totalUsage, p.Err
		}

		if len(results) == 0 {
			break
		}

		if onStep == nil {
			continue
		}

		inject, stop := onStep(step, text, results)
		if inject != "" {
			w.Inject(processID, inject)
		}
		if stop {
			text, _, usage = w.Step(ctx, processID)
			totalUsage.InputTokens += usage.InputTokens
			totalUsage.OutputTokens += usage.OutputTokens
			lastText = text
			break
		}
	}

	return lastText, totalUsage, nil
}

func (w *Workflow) Run(ctx context.Context, purpose string, task string) (string, TokenUsage, error) {
	caps := make([]Capability, len(w.tools))
	for i, t := range w.tools {
		caps[i] = Capability(t.Name)
	}

	p := w.Spawn(purpose, purpose, caps, "")

	defs := make([]ToolDefinition, len(w.tools))
	for i, t := range w.tools {
		defs[i] = ToolDefinition{Name: t.Name, Description: t.Description, Params: t.Params}
	}
	p.Tools = defs

	p.View.Filter = func(e Event) bool {
		return e.ProcessID == p.ID
	}

	w.Inject(p.ID, task)
	return w.StepUntilDone(ctx, p.ID, 50, nil)
}

type EffectResult struct {
	Tool      string
	ToolUseID string
	Admitted  bool
	Result    string
	Error     string
}

func (w *Workflow) processEffect(p *Process, pe ProposedEffect) EffectResult {
	cacheKey := effectCacheKey(pe)
	if p.EffectCache != nil {
		if cached, ok := p.EffectCache[cacheKey]; ok {
			cached.ToolUseID = pe.ToolUseID
			return cached
		}
	}

	eff := &Effect{
		ID:        w.newEffectID(),
		ProcessID: p.ID,
		Tool:      pe.Tool,
		Args:      pe.Args,
		State:     EffectPlanned,
	}

	intentEventID := w.Log.Append(Event{
		Type:      EventEffectIntent,
		ProcessID: p.ID,
		Data:      mergeMap(map[string]string{"tool": pe.Tool, "effect_id": eff.ID}, pe.Args),
	})
	eff.EventID = intentEventID
	w.Effects.Track(eff)

	if !p.HasCapability(Capability(pe.Tool)) {
		eff.State = EffectRejected
		reason := fmt.Sprintf("process lacks capability: %s", pe.Tool)
		w.Log.Append(Event{
			Type:      EventEffectRejected,
			ProcessID: p.ID,
			Data:      map[string]string{"effect_id": eff.ID, "reason": reason},
		})
		return EffectResult{Tool: pe.Tool, ToolUseID: pe.ToolUseID, Admitted: false, Error: reason}
	}

	if ok, reason := w.Effects.CheckPolicies(p, eff); !ok {
		eff.State = EffectRejected
		w.Log.Append(Event{
			Type:      EventEffectRejected,
			ProcessID: p.ID,
			Data:      map[string]string{"effect_id": eff.ID, "reason": reason},
		})
		return EffectResult{Tool: pe.Tool, ToolUseID: pe.ToolUseID, Admitted: false, Error: reason}
	}

	eff.State = EffectAdmitted

	var toolDef *Tool
	for i := range w.tools {
		if w.tools[i].Name == pe.Tool {
			toolDef = &w.tools[i]
			break
		}
	}

	if toolDef != nil {
		if reason := validateArgs(toolDef.Params, pe.Args); reason != "" {
			eff.State = EffectRejected
			w.Log.Append(Event{
				Type:      EventEffectRejected,
				ProcessID: p.ID,
				Data:      map[string]string{"effect_id": eff.ID, "reason": reason},
			})
			return EffectResult{Tool: pe.Tool, ToolUseID: pe.ToolUseID, Admitted: true, Error: reason}
		}
	}

	handler, ok := w.Effects.GetHandler(pe.Tool)
	if !ok {
		eff.State = EffectRejected
		reason := fmt.Sprintf("no handler for tool: %s", pe.Tool)
		w.Log.Append(Event{
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
		w.Log.Append(Event{
			Type:      EventEffectRejected,
			ProcessID: p.ID,
			Data:      map[string]string{"effect_id": eff.ID, "reason": err.Error(), "duration_ms": strconv.FormatInt(duration.Milliseconds(), 10)},
		})
		return EffectResult{Tool: pe.Tool, ToolUseID: pe.ToolUseID, Admitted: true, Error: err.Error()}
	}

	eff.State = EffectObserved
	eff.Result = handlerResult
	w.Log.Append(Event{
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

func validateArgs(params map[string]Param, args map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	var missing []string
	for name, p := range params {
		if p.Required && args[name] == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Sprintf("missing required parameters: %s", strings.Join(missing, ", "))
	}
	var unknown []string
	for k := range args {
		if _, ok := params[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		var known []string
		for name := range params {
			known = append(known, name)
		}
		sort.Strings(known)
		return fmt.Sprintf("unknown parameters: %s (valid: %s)", strings.Join(unknown, ", "), strings.Join(known, ", "))
	}
	return ""
}
