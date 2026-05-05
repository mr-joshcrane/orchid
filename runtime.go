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

func (w *Workflow) dispatchParallel(ctx context.Context, p *Process, effects []ProposedEffect) ([]EffectResult, error) {
	results := make([]EffectResult, len(effects))
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
				r, fatal := w.processEffect(gctx, p, effects[idx])
				mu.Lock()
				results[idx] = r
				mu.Unlock()
				return fatal
			})
		}
		return g.Wait()
	}

	var batch []int
	for i, pe := range effects {
		if w.isParallelizable(pe.Tool) {
			batch = append(batch, i)
			continue
		}
		if err := flushBatch(batch); err != nil {
			fatalErr = err
			return results, fatalErr
		}
		batch = nil
		r, fatal := w.processEffect(ctx, p, effects[i])
		results[i] = r
		if fatal != nil {
			return results, fatal
		}
	}
	if err := flushBatch(batch); err != nil {
		fatalErr = err
	}

	return results, fatalErr
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
	History  json.RawMessage  `json:"history"`
	Usage    TokenUsage       `json:"usage"`
	Tools    []ToolDefinition `json:"tools"`
}

func (w *Workflow) SaveSession(wr io.Writer) error {
	data := sessionData{
		Events: w.Log.All(),
	}
	for _, p := range w.Processes.All() {
		sp := sessionProcess{
			ID:       p.ID,
			Name:     p.Name,
			Purpose:  p.Purpose,
			State:    p.State,
			ParentID: p.ParentID,
			Usage:    p.Usage,
			Tools:    p.Tools,
		}
		if hs, ok := w.gen.(HistorySerializer); ok && p.History != nil {
			histBytes, err := hs.MarshalHistory(p.History)
			if err != nil {
				return fmt.Errorf("marshaling history for process %s: %w", p.ID, err)
			}
			sp.History = histBytes
		}
		data.Processes = append(data.Processes, sp)
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
		if hs, ok := w.gen.(HistorySerializer); ok && sp.History != nil {
			hist, err := hs.UnmarshalHistory(sp.History)
			if err != nil {
				return fmt.Errorf("unmarshaling history for process %s: %w", sp.ID, err)
			}
			p.History = hist
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
	var fatalErr error
	if w.concurrency <= 1 {
		for _, pe := range proposal.Effects {
			result, fatal := w.processEffect(ctx, p, pe)
			results = append(results, result)
			if fatal != nil {
				fatalErr = fatal
				break
			}
		}
	} else {
		results, fatalErr = w.dispatchParallel(ctx, p, proposal.Effects)
	}

	p.PendingResults = results

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

func (w *Workflow) invokeHandler(ctx context.Context, p *Process, pe ProposedEffect) (string, bool, error) {
	if ch, ok := w.Effects.GetContextualHandler(pe.Tool); ok {
		handler := applyMiddleware(ch, w.middleware)
		hctx := HandlerContext{
			Ctx:       ctx,
			ProcessID: p.ID,
			ToolName:  pe.Tool,
			RootDir:   w.rootDir,
			LogAppend: func(e Event) { w.Log.Append(e) },
		}
		result, err := handler(hctx, pe.Args)
		return result, true, err
	}
	if h, ok := w.Effects.GetHandler(pe.Tool); ok {
		handler := applyMiddleware(adaptLegacyHandler(h), w.middleware)
		hctx := HandlerContext{
			Ctx:       ctx,
			ProcessID: p.ID,
			ToolName:  pe.Tool,
			RootDir:   w.rootDir,
			LogAppend: func(e Event) { w.Log.Append(e) },
		}
		result, err := handler(hctx, pe.Args)
		return result, true, err
	}
	return "", false, nil
}

func (w *Workflow) processEffect(ctx context.Context, p *Process, pe ProposedEffect) (EffectResult, error) {
	cacheKey := effectCacheKey(pe)
	p.cacheMu.Lock()
	if p.EffectCache != nil {
		if cached, ok := p.EffectCache[cacheKey]; ok {
			cached.ToolUseID = pe.ToolUseID
			p.cacheMu.Unlock()
			return cached, nil
		}
	}
	p.cacheMu.Unlock()

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

	if !w.hasToolPermission(p, ToolName(pe.Tool)) {
		eff.State = EffectRejected
		reason := fmt.Sprintf("process lacks permission: tool:%s", pe.Tool)
		w.Log.Append(Event{
			Type:      EventEffectRejected,
			ProcessID: p.ID,
			Data:      map[string]string{"effect_id": eff.ID, "reason": reason},
		})
		return EffectResult{Tool: pe.Tool, ToolUseID: pe.ToolUseID, Admitted: false, Error: reason}, nil
	}

	if ok, reason := w.Effects.CheckPolicies(p, eff); !ok {
		eff.State = EffectRejected
		w.Log.Append(Event{
			Type:      EventEffectRejected,
			ProcessID: p.ID,
			Data:      map[string]string{"effect_id": eff.ID, "reason": reason},
		})
		return EffectResult{Tool: pe.Tool, ToolUseID: pe.ToolUseID, Admitted: false, Error: reason}, nil
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
			return EffectResult{Tool: pe.Tool, ToolUseID: pe.ToolUseID, Admitted: true, Error: reason}, nil
		}
	}

	_, hasLegacy := w.Effects.GetHandler(pe.Tool)
	_, hasCtx := w.Effects.GetContextualHandler(pe.Tool)
	if !hasLegacy && !hasCtx {
		eff.State = EffectRejected
		reason := fmt.Sprintf("no handler for tool: %s", pe.Tool)
		w.Log.Append(Event{
			Type:      EventEffectRejected,
			ProcessID: p.ID,
			Data:      map[string]string{"effect_id": eff.ID, "reason": reason},
		})
		return EffectResult{Tool: pe.Tool, ToolUseID: pe.ToolUseID, Admitted: true, Error: reason}, nil
	}

	eff.State = EffectStarted

	start := time.Now()
	handlerResult, _, err := w.invokeHandler(ctx, p, pe)
	duration := time.Since(start)
	if err != nil {
		eff.State = EffectRejected
		eff.Result = err.Error()
		w.Log.Append(Event{
			Type:      EventEffectRejected,
			ProcessID: p.ID,
			Data:      map[string]string{"effect_id": eff.ID, "reason": err.Error(), "duration_ms": strconv.FormatInt(duration.Milliseconds(), 10)},
		})
		var fatal error
		if errors.Is(err, ErrEffectFatal) {
			fatal = ErrEffectFatal
		}
		return EffectResult{Tool: pe.Tool, ToolUseID: pe.ToolUseID, Admitted: true, Error: err.Error()}, fatal
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
	p.cacheMu.Lock()
	if p.EffectCache == nil {
		p.EffectCache = make(map[string]EffectResult)
	}
	p.EffectCache[cacheKey] = er
	p.cacheMu.Unlock()
	return er, nil
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
