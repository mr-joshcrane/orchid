package orchid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

type Workflow struct {
	Log          *EventLog
	Processes    *ProcessTable
	Effects      *EffectTable
	gen          Generator
	tools        []Tool
	middleware   []ToolMiddleware
	running      atomic.Bool
	rootDir      string
	logDir       string
	logFile      *os.File
	logTimestamp string
	concurrency  int
	configErr    error
	permBundles  *permissionBundleRegistry
	idPrefix     string
	nextPID      atomic.Uint64
	sqliteSink   *SQLiteSink
}

var ErrEffectFatal = errors.New("orchid: fatal effect error")

func (w *Workflow) isParallelizable(toolName string) bool {
	for i := range w.tools {
		if w.tools[i].Name == toolName {
			return w.tools[i].Parallelizable
		}
	}
	return false
}

// dispatchParallel runs planned effects, batching consecutive parallelizable
// ones. A non-parallelizable effect drains the current batch first. Results
// keep model order regardless of completion order, and an effect that never
// ran carries an explicit error rather than a zero value, so the derived
// transcript answers every tool call.
func (w *Workflow) dispatchParallel(ctx context.Context, p *Process, planned []plannedEffect) ([]EffectResult, error) {
	results := make([]EffectResult, len(planned))
	var mu sync.Mutex
	var fatalErr error

	flushBatch := func(batch []int) error {
		if len(batch) == 0 {
			return nil
		}
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(w.concurrency)
		for _, idx := range batch {
			g.Go(func() error {
				r, fatal := w.processEffect(gctx, p, planned[idx])
				mu.Lock()
				results[idx] = r
				mu.Unlock()
				return fatal
			})
		}
		return g.Wait()
	}

	var batch []int
	for i, pl := range planned {
		if w.isParallelizable(pl.proposed.Tool) {
			batch = append(batch, i)
			continue
		}
		if err := flushBatch(batch); err != nil {
			fatalErr = err
			return w.completeUnrun(p, planned, results), fatalErr
		}
		batch = nil
		r, fatal := w.processEffect(ctx, p, planned[i])
		results[i] = r
		if fatal != nil {
			return w.completeUnrun(p, planned, results), fatal
		}
	}
	if err := flushBatch(batch); err != nil {
		fatalErr = err
	}

	return w.completeUnrun(p, planned, results), fatalErr
}

// completeUnrun records a rejection for every planned effect that produced no
// result, so an abandoned batch cannot leave a tool call unanswered.
func (w *Workflow) completeUnrun(p *Process, planned []plannedEffect, results []EffectResult) []EffectResult {
	for i := range results {
		if results[i].Tool != "" {
			continue
		}
		results[i], _ = w.rejectEffect(p, planned[i], "effect not run: dispatch stopped", true, 0)
	}
	return results
}

type WorkflowOption func(*Workflow)

func WithTools(tools ...Tool) WorkflowOption {
	return func(w *Workflow) {
		for _, t := range tools {
			if err := w.Effects.RegisterTool(t); err != nil {
				panic(err.Error())
			}
			w.tools = append(w.tools, t)
		}
	}
}

func WithRootDir(dir string) WorkflowOption {
	return func(w *Workflow) {
		abs, err := filepath.Abs(dir)
		if err != nil {
			w.configErr = fmt.Errorf("orchid: WithRootDir: %w", err)
			return
		}
		info, err := os.Stat(abs)
		if err != nil {
			w.configErr = fmt.Errorf("orchid: WithRootDir: %w", err)
			return
		}
		if !info.IsDir() {
			w.configErr = fmt.Errorf("orchid: WithRootDir: %s is not a directory", abs)
			return
		}
		w.rootDir = abs
	}
}

func WithLogDir(dir string) WorkflowOption {
	return func(w *Workflow) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			w.configErr = fmt.Errorf("orchid: WithLogDir: %w", err)
			return
		}
		ts := time.Now().UTC().Format("20060102T150405Z")
		path := filepath.Join(dir, ts+"-events.jsonl")
		f, err := os.Create(path)
		if err != nil {
			w.configErr = fmt.Errorf("orchid: WithLogDir: %w", err)
			return
		}
		w.logDir = dir
		w.logFile = f
		w.logTimestamp = ts
		w.Log.SetSink(f)
	}
}

// WithSQLiteLog configures a SQLite event sink at the given path. The database
// file is created if it does not exist. When using together with WithLogDir,
// call WithLogDir first so that the parent directory exists.
//
// Requires building with -tags sqlite; without the tag the option returns an
// error from NewWorkflowWithOptions.
func WithSQLiteLog(path string) WorkflowOption {
	return func(w *Workflow) {
		s, err := newSQLiteSink(path)
		if err != nil {
			w.configErr = fmt.Errorf("orchid: WithSQLiteLog(%q): %w", path, err)
			return
		}
		w.sqliteSink = s
		w.Log.AddSink(s)
	}
}

func WithEffectConcurrency(n int) WorkflowOption {
	return func(w *Workflow) {
		w.concurrency = n
	}
}

func NewWorkflow(gen Generator, tools ...Tool) (*Workflow, error) {
	wf := &Workflow{
		Log:         NewEventLog(),
		Processes:   NewProcessTable(),
		Effects:     NewEffectTable(),
		gen:         gen,
		tools:       tools,
		permBundles: newPermissionBundleRegistry(),
	}
	for _, t := range tools {
		wf.Effects.RegisterHandler(t.Name, t.Handler)
	}
	return wf, nil
}

func NewWorkflowWithOptions(gen Generator, opts ...WorkflowOption) (*Workflow, error) {
	wf := &Workflow{
		Log:         NewEventLog(),
		Processes:   NewProcessTable(),
		Effects:     NewEffectTable(),
		gen:         gen,
		permBundles: newPermissionBundleRegistry(),
	}
	for _, opt := range opts {
		opt(wf)
	}
	if wf.configErr != nil {
		return nil, wf.configErr
	}
	return wf, nil
}

func (w *Workflow) Close() error {
	var errs []error
	if w.logFile != nil {
		errs = append(errs, w.logFile.Close())

		events := w.Log.All()
		auditPath := filepath.Join(w.logDir, w.logTimestamp+"-audit.txt")
		if f, err := os.Create(auditPath); err == nil {
			PrintLog(f, events)
			errs = append(errs, f.Close())
		} else {
			errs = append(errs, err)
		}

		treePath := filepath.Join(w.logDir, w.logTimestamp+"-tree.txt")
		if f, err := os.Create(treePath); err == nil {
			PrintTree(f, BuildProcessTree(events))
			errs = append(errs, f.Close())
		} else {
			errs = append(errs, err)
		}

		w.logFile = nil
	}
	if w.sqliteSink != nil {
		errs = append(errs, w.sqliteSink.Close())
		w.sqliteSink = nil
	}
	return errors.Join(errs...)
}

type sessionData struct {
	Events    []Event          `json:"events"`
	Processes []sessionProcess `json:"processes"`
}

type sessionProcess struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Purpose  string           `json:"purpose"`
	State    ProcessState     `json:"state"`
	ParentID string           `json:"parent_id"`
	Usage    TokenUsage       `json:"usage"`
	Tools    []ToolDefinition `json:"tools"`
}

// SaveSession writes the events and process table. Conversation history is not
// stored: it derives from the events, so a restored session reconstructs the
// exact transcript, including tool results for calls that had already run.
func (w *Workflow) SaveSession(wr io.Writer) error {
	data := sessionData{
		Events: w.Log.All(),
	}
	for _, p := range w.Processes.All() {
		data.Processes = append(data.Processes, sessionProcess{
			ID:       p.ID,
			Name:     p.Name,
			Purpose:  p.Purpose,
			State:    p.State,
			ParentID: p.ParentID,
			Usage:    p.Usage,
			Tools:    p.Tools,
		})
	}
	return json.NewEncoder(wr).Encode(data)
}

func (w *Workflow) LoadSession(r io.Reader) error {
	var data sessionData
	if err := json.NewDecoder(r).Decode(&data); err != nil {
		return fmt.Errorf("decoding session: %w", err)
	}
	for _, e := range data.Events {
		w.Log.Append(e)
	}
	for _, sp := range data.Processes {
		p := &Process{
			ID:           sp.ID,
			Name:         sp.Name,
			Purpose:      sp.Purpose,
			State:        sp.State,
			ParentID:     sp.ParentID,
			Capabilities: make(map[Capability]bool),
			Permissions:  make(map[Permission]bool),
			Usage:        sp.Usage,
			Tools:        sp.Tools,
			View: LogView{
				Source: w.Log,
				Filter: func(e Event) bool { return e.ProcessID == sp.ID },
			},
		}
		for _, td := range sp.Tools {
			p.Capabilities[Capability(td.Name)] = true
			p.Permissions[PermissionFromTool(ToolName(td.Name))] = true
		}
		w.Processes.Add(p)
	}
	return nil
}

// Use appends workflow-global middleware applied to every tool at dispatch time.
// Panics if called after Run has started.
func (w *Workflow) Use(mw ...ToolMiddleware) {
	if w.running.Load() {
		panic("orchid: Workflow.Use called after Run started")
	}
	w.middleware = append(w.middleware, mw...)
}

func (w *Workflow) RegisterPermissionBundle(p Permission, tools ...ToolName) {
	w.permBundles.register(p, tools...)
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
		Permissions:  make(map[Permission]bool),
		View: LogView{
			Source:   w.Log,
			StartCut: uint64(w.Log.Len()),
		},
		State:    ProcessReady,
		ParentID: parentID,
	}
	for _, c := range caps {
		p.Capabilities[c] = true
		p.Permissions[PermissionFromTool(ToolName(c))] = true
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

	w.emitPromptTelemetry(gen, orchidCtx, processID)

	proposal := gen.Generate(orchidCtx)

	if proposal.Err != nil {
		p.State = ProcessFailed
		p.Err = proposal.Err
		return "", nil, TokenUsage{}
	}

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

	planned := w.planEffects(p, proposal.Effects)

	var results []EffectResult
	var fatalErr error
	if w.concurrency <= 1 {
		for _, pl := range planned {
			result, fatal := w.processEffect(ctx, p, pl)
			results = append(results, result)
			if fatal != nil {
				fatalErr = fatal
				break
			}
		}
	} else {
		results, fatalErr = w.dispatchParallel(ctx, p, planned)
	}

	if fatalErr != nil {
		p.State = ProcessFailed
		p.Err = fatalErr
		return proposal.Text, results, TokenUsage{InputTokens: proposal.InputTokens, OutputTokens: proposal.OutputTokens}
	}

	p.State = ProcessWaiting
	return proposal.Text, results, TokenUsage{InputTokens: proposal.InputTokens, OutputTokens: proposal.OutputTokens}
}

func (w *Workflow) emitPromptTelemetry(gen Generator, orchidCtx Context, processID string) {
	var est int
	if te, ok := gen.(TokenEstimator); ok {
		est = te.EstimateTokens(orchidCtx)
	} else {
		est = defaultEstimateTokens(orchidCtx)
	}

	var limit int
	if ml, ok := gen.(ModelLimiter); ok {
		limit = ml.ModelLimit()
	} else if mi, ok := gen.(ModelIdentifier); ok {
		limit = lookupModelLimit(mi.Model())
	}

	utilisation := 0
	if limit > 0 {
		utilisation = est * 100 / limit
	}

	w.Log.Append(Event{
		Type:      EventPromptSize,
		ProcessID: processID,
		Data: map[string]string{
			"estimated_tokens": strconv.Itoa(est),
			"model_limit":      strconv.Itoa(limit),
			"utilisation_pct":  strconv.Itoa(utilisation),
		},
	})

	if limit > 0 && est > limit*95/100 {
		w.Log.Append(Event{
			Type:      EventPromptPressure,
			ProcessID: processID,
			Data:      map[string]string{"level": "critical", "utilisation_pct": strconv.Itoa(utilisation)},
		})
	} else if limit > 0 && est > limit*80/100 {
		w.Log.Append(Event{
			Type:      EventPromptPressure,
			ProcessID: processID,
			Data:      map[string]string{"level": "warn", "utilisation_pct": strconv.Itoa(utilisation)},
		})
	}
}

type StepCallback func(step int, text string, results []EffectResult) (inject string, stop bool)

type Constraints struct {
	MaxSteps  int
	MaxTokens int
}

type RunOption func(*Constraints)

func WithConstraints(c Constraints) RunOption {
	return func(dst *Constraints) {
		if c.MaxSteps > 0 {
			dst.MaxSteps = c.MaxSteps
		}
		if c.MaxTokens > 0 {
			dst.MaxTokens = c.MaxTokens
		}
	}
}

func (w *Workflow) StepUntilDone(ctx context.Context, processID string, c Constraints, onStep StepCallback) (string, TokenUsage, error) {
	var totalUsage TokenUsage
	var lastText string

	maxSteps := c.MaxSteps
	if maxSteps == 0 {
		maxSteps = 50
	}

	for step := 0; step < maxSteps; step++ {
		text, results, usage := w.Step(ctx, processID)
		totalUsage.InputTokens += usage.InputTokens
		totalUsage.OutputTokens += usage.OutputTokens
		lastText = text

		p := w.Processes.Get(processID)
		if p != nil && p.Err != nil {
			return "", totalUsage, p.Err
		}

		if c.MaxTokens > 0 && totalUsage.InputTokens+totalUsage.OutputTokens >= c.MaxTokens {
			break
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

func (w *Workflow) Run(ctx context.Context, purpose string, task string, opts ...RunOption) (string, TokenUsage, error) {
	w.running.Store(true)
	c := Constraints{}
	for _, opt := range opts {
		opt(&c)
	}

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
	return w.StepUntilDone(ctx, p.ID, c, nil)
}

func MustRun(ctx context.Context, gen Generator, purpose, task string, opts ...WorkflowOption) (string, TokenUsage, error) {
	wf, err := NewWorkflowWithOptions(gen, opts...)
	if err != nil {
		return "", TokenUsage{}, err
	}
	defer func() {
		err = errors.Join(err, wf.Close())
	}()
	text, usage, runErr := wf.Run(ctx, purpose, task)
	return text, usage, runErr
}

type EffectResult struct {
	Tool      string
	ToolUseID string
	Admitted  bool
	Result    string
	Error     string
}

// plannedEffect is a proposed call with its tracked Effect and logged intent.
type plannedEffect struct {
	proposed ProposedEffect
	effect   *Effect
}

// planEffects assigns ids and logs one intent per call in model order, before
// any dispatch. Logging intents up front is what lets DeriveMessages rebuild
// the assistant message in the order the model proposed it, independent of the
// order parallel dispatch happens to complete in.
func (w *Workflow) planEffects(p *Process, proposed []ProposedEffect) []plannedEffect {
	planned := make([]plannedEffect, len(proposed))
	for i, pe := range proposed {
		eff := &Effect{
			ID:        w.newEffectID(),
			ProcessID: p.ID,
			Tool:      pe.Tool,
			Args:      pe.Args,
			State:     EffectPlanned,
		}
		// A generator that proposes no id still needs one: the derived
		// transcript pairs results to calls by it.
		if pe.ToolUseID == "" {
			pe.ToolUseID = eff.ID
		}
		eff.ToolUseID = pe.ToolUseID
		eff.EventID = w.Log.Append(Event{
			Type:      EventEffectIntent,
			ProcessID: p.ID,
			Data:      intentData(pe, eff.ID),
		})
		w.Effects.Track(eff)
		planned[i] = plannedEffect{proposed: pe, effect: eff}
	}
	return planned
}

// intentData records a call's identity and arguments. Arguments are namespaced
// under ArgPrefix so an argument named "tool" or "effect_id" cannot overwrite
// the event's own metadata.
func intentData(pe ProposedEffect, effectID string) map[string]string {
	data := map[string]string{
		KeyTool:      pe.Tool,
		KeyEffectID:  effectID,
		KeyToolUseID: pe.ToolUseID,
	}
	for k, v := range pe.Args {
		data[ArgPrefix+k] = v
	}
	return data
}

func (w *Workflow) invokeHandler(ctx context.Context, p *Process, pl plannedEffect) (string, error) {
	pe := pl.proposed
	hctx := HandlerContext{
		Ctx:       ctx,
		ProcessID: p.ID,
		ToolName:  pe.Tool,
		RootDir:   w.rootDir,
		LogAppend: func(e Event) { w.Log.Append(e) },
		EffectID:  pl.effect.ID,
		ToolUseID: pe.ToolUseID,
	}
	return applyMiddleware(w.handlerFor(pe.Tool), w.middleware)(hctx, pe.Args)
}

// handlerFor resolves a tool's handler, preferring a contextual registration
// and adapting a legacy one. Callers check hasHandler first.
func (w *Workflow) handlerFor(tool string) ContextualEffectHandler {
	if ch, ok := w.Effects.GetContextualHandler(tool); ok {
		return ch
	}
	h, _ := w.Effects.GetHandler(tool)
	return adaptLegacyHandler(h)
}

func (w *Workflow) hasHandler(tool string) bool {
	if _, ok := w.Effects.GetContextualHandler(tool); ok {
		return true
	}
	_, ok := w.Effects.GetHandler(tool)
	return ok
}

// logObserved records a completed call's result. Every answered call gets one,
// including a cache hit, so the derived transcript answers every tool call.
func (w *Workflow) logObserved(p *Process, pl plannedEffect, result string, duration time.Duration) {
	w.Log.Append(Event{
		Type:      EventEffectObserved,
		ProcessID: p.ID,
		Data: map[string]string{
			KeyEffectID:   pl.effect.ID,
			KeyTool:       pl.proposed.Tool,
			KeyToolUseID:  pl.proposed.ToolUseID,
			KeyResult:     result,
			"duration_ms": strconv.FormatInt(duration.Milliseconds(), 10),
		},
	})
}

// rejectEffect records a refusal and returns the error result the model sees.
// admitted distinguishes a call refused before admission (permission, policy)
// from one that was admitted and then failed (bad arguments, missing handler,
// handler error).
func (w *Workflow) rejectEffect(
	p *Process, pl plannedEffect, reason string, admitted bool, duration time.Duration,
) (EffectResult, error) {
	pl.effect.State = EffectRejected
	pl.effect.Result = reason
	w.Log.Append(Event{
		Type:      EventEffectRejected,
		ProcessID: p.ID,
		Data: map[string]string{
			KeyEffectID:   pl.effect.ID,
			KeyTool:       pl.proposed.Tool,
			KeyToolUseID:  pl.proposed.ToolUseID,
			KeyReason:     reason,
			"duration_ms": strconv.FormatInt(duration.Milliseconds(), 10),
		},
	})
	return EffectResult{
		Tool:      pl.proposed.Tool,
		ToolUseID: pl.proposed.ToolUseID,
		Admitted:  admitted,
		Error:     reason,
	}, nil
}

// cachedResult returns a previously observed result for an identical call.
func (w *Workflow) cachedResult(p *Process, pe ProposedEffect) (EffectResult, bool) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	cached, ok := p.EffectCache[effectCacheKey(pe)]
	if !ok {
		return EffectResult{}, false
	}
	cached.ToolUseID = pe.ToolUseID
	return cached, true
}

func (w *Workflow) storeResult(p *Process, pe ProposedEffect, result EffectResult) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	if p.EffectCache == nil {
		p.EffectCache = make(map[string]EffectResult)
	}
	p.EffectCache[effectCacheKey(pe)] = result
}

// validateEffectArgs checks the call against its tool's declared parameters,
// returning a refusal reason or the empty string. A call for an unregistered
// tool validates trivially; the missing-handler check refuses it.
func (w *Workflow) validateEffectArgs(pe ProposedEffect) string {
	for i := range w.tools {
		if w.tools[i].Name == pe.Tool {
			return validateArgs(w.tools[i].Params, pe.Args)
		}
	}
	return ""
}

// processEffect admits, dispatches, and observes one planned call. Its intent is
// already logged; every exit records an observation or a rejection, so the
// derived transcript answers the call.
func (w *Workflow) processEffect(ctx context.Context, p *Process, pl plannedEffect) (EffectResult, error) {
	pe := pl.proposed
	eff := pl.effect

	if cached, ok := w.cachedResult(p, pe); ok {
		w.logObserved(p, pl, cached.Result, 0)
		eff.State = EffectApplied
		return cached, nil
	}

	if !w.hasToolPermission(p, ToolName(pe.Tool)) {
		return w.rejectEffect(p, pl, fmt.Sprintf("process lacks permission: tool:%s", pe.Tool), false, 0)
	}
	if ok, reason := w.Effects.CheckPolicies(p, eff); !ok {
		return w.rejectEffect(p, pl, reason, false, 0)
	}

	eff.State = EffectAdmitted

	if reason := w.validateEffectArgs(pe); reason != "" {
		return w.rejectEffect(p, pl, reason, true, 0)
	}
	if !w.hasHandler(pe.Tool) {
		return w.rejectEffect(p, pl, fmt.Sprintf("no handler for tool: %s", pe.Tool), true, 0)
	}

	eff.State = EffectStarted
	start := time.Now()
	handlerResult, err := w.invokeHandler(ctx, p, pl)
	duration := time.Since(start)
	if err != nil {
		result, _ := w.rejectEffect(p, pl, err.Error(), true, duration)
		if errors.Is(err, ErrEffectFatal) {
			return result, ErrEffectFatal
		}
		return result, nil
	}

	eff.State = EffectObserved
	eff.Result = handlerResult
	w.logObserved(p, pl, handlerResult, duration)
	eff.State = EffectApplied

	result := EffectResult{Tool: pe.Tool, ToolUseID: pe.ToolUseID, Admitted: true, Result: handlerResult}
	w.storeResult(p, pe, result)
	return result, nil
}

func (w *Workflow) hasToolPermission(p *Process, tool ToolName) bool {
	toolPerm := PermissionFromTool(tool)
	if p.HasPermission(toolPerm) {
		return true
	}
	for perm := range p.Permissions {
		if w.permBundles.expandsTo(perm, tool) {
			return true
		}
	}
	if p.HasCapability(Capability(tool)) {
		w.Log.Append(Event{
			Type:      EventPermissionFallback,
			ProcessID: p.ID,
			Data:      map[string]string{"tool": string(tool)},
		})
		return true
	}
	return false
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
