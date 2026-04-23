package orchid

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var DefaultLogDir string
var logSeq atomic.Uint64

func init() {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	DefaultLogDir = filepath.Join(home, ".orchid", "logs")
}

type Workflow struct {
	Log              *EventLog
	Processes        *ProcessTable
	Effects          *EffectTable
	DefaultGenerator Generator
	nextPID          atomic.Uint64
	logCh            chan Event
	done             chan struct{}
}

func NewWorkflow() *Workflow {
	wf := &Workflow{
		Log:       NewEventLog(),
		Processes: NewProcessTable(),
		Effects:   NewEffectTable(),
	}
	wf.startLogWriter()
	wf.Log.SetListener(wf.logCh)
	return wf
}

func NewRuntime(gen Generator) *Workflow {
	wf := NewWorkflow()
	wf.DefaultGenerator = gen
	return wf
}

func (w *Workflow) Quiet() {
	w.Log.SetListener(nil)
}

func (w *Workflow) Close() {
	w.Log.SetListener(nil)
	if w.logCh != nil {
		close(w.logCh)
		<-w.done
		w.logCh = nil
	}
}

func (w *Workflow) startLogWriter() {
	if DefaultLogDir == "" {
		return
	}
	err := os.MkdirAll(DefaultLogDir, 0o755)
	if err != nil {
		return
	}
	name := fmt.Sprintf("%s-%d-%d.jsonl", time.Now().Format("2006-01-02T15-04-05.000"), os.Getpid(), logSeq.Add(1))
	f, err := os.Create(filepath.Join(DefaultLogDir, name))
	if err != nil {
		return
	}

	w.logCh = make(chan Event, 1000)
	w.done = make(chan struct{})

	go func() {
		defer f.Close()
		defer close(w.done)
		for e := range w.logCh {
			data, err := json.Marshal(eventRecord{
				ID:        e.ID,
				Type:      e.Type.String(),
				Timestamp: e.Timestamp,
				ProcessID: e.ProcessID,
				Data:      e.Data,
			})
			if err != nil {
				continue
			}
			f.Write(append(data, '\n'))
		}
	}()
}

func (w *Workflow) newProcessID() string {
	return fmt.Sprintf("proc_%d", w.nextPID.Add(1))
}

func (w *Workflow) newEffectID() string {
	return fmt.Sprintf("eff_%d", w.nextPID.Add(1))
}

func (w *Workflow) Spawn(spec ProcessSpec, caps []Capability, parentID string) *Process {
	p := &Process{
		ID:           w.newProcessID(),
		Spec:         spec,
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
			"name":          spec.Name,
			"parent":        parentID,
			"model":         spec.Model,
			"system_prompt": spec.SystemPrompt,
		},
	})
	return p
}

func (w *Workflow) Fork(parentID string, childSpec ProcessSpec, caps []Capability) *Process {
	child := w.Spawn(childSpec, caps, parentID)
	child.View.StartCut = 0
	child.View.Source = w.Log

	w.Log.Append(Event{
		Type:      EventFork,
		ProcessID: parentID,
		Data:      map[string]string{"child_id": child.ID},
	})
	return child
}

func (w *Workflow) Join(parentID string, childID string, result string, usage TokenUsage) {
	w.Log.Append(Event{
		Type:      EventJoin,
		ProcessID: parentID,
		Data: map[string]string{
			"child_id":      childID,
			"result":        result,
			"input_tokens":  strconv.Itoa(usage.InputTokens),
			"output_tokens": strconv.Itoa(usage.OutputTokens),
		},
	})

	child := w.Processes.Get(childID)
	if child != nil {
		child.State = ProcessDone
		w.Log.Append(Event{
			Type:      EventProcessExited,
			ProcessID: childID,
		})
	}
}

func (w *Workflow) Inject(processID string, input string) {
	w.Log.Append(Event{
		Type:      EventUserInput,
		ProcessID: processID,
		Data:      map[string]string{"content": input},
	})
}

func (w *Workflow) Send(from, to, content string) {
	w.Log.Append(Event{
		Type:      EventMessage,
		ProcessID: to,
		Data:      map[string]string{"from": from, "content": content},
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
		gen = w.DefaultGenerator
	}
	proposal := gen.Generate(orchidCtx)

	p.History = proposal.History
	p.Usage.InputTokens += proposal.InputTokens
	p.Usage.OutputTokens += proposal.OutputTokens

	model := p.Spec.Model
	if model == "" {
		if mi, ok := gen.(ModelIdentifier); ok {
			model = mi.Model()
		}
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

	handler, ok := p.GetHandler(pe.Tool)
	if !ok {
		handler, ok = w.Effects.GetHandler(pe.Tool)
	}
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
