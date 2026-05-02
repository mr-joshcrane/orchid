package orchid

import (
	"context"
	"testing"
)

func TestPromptSizeEventEmitted(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{Text: "done"}
	})

	wf, err := NewWorkflow(gen)
	if err != nil {
		t.Fatal(err)
	}

	p := wf.Spawn("test", "test purpose", nil, "")
	wf.Inject(p.ID, "hello world")
	wf.Step(context.Background(), p.ID)

	var found bool
	for _, e := range wf.Log.All() {
		if e.Type == EventPromptSize {
			found = true
			if e.Data["estimated_tokens"] == "" {
				t.Fatal("estimated_tokens should be set")
			}
		}
	}
	if !found {
		t.Fatal("expected EventPromptSize event")
	}
}

func TestPromptPressureWarnEmitted(t *testing.T) {
	gen := &fakeEstimatorGen{
		estimate: 850,
		limit:    1000,
	}

	wf, err := NewWorkflow(gen)
	if err != nil {
		t.Fatal(err)
	}

	p := wf.Spawn("test", "test", nil, "")
	wf.Inject(p.ID, "go")
	wf.Step(context.Background(), p.ID)

	var found bool
	for _, e := range wf.Log.All() {
		if e.Type == EventPromptPressure && e.Data["level"] == "warn" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected EventPromptPressure with level=warn at 85% utilisation")
	}
}

func TestPromptPressureCriticalEmitted(t *testing.T) {
	gen := &fakeEstimatorGen{
		estimate: 960,
		limit:    1000,
	}

	wf, err := NewWorkflow(gen)
	if err != nil {
		t.Fatal(err)
	}

	p := wf.Spawn("test", "test", nil, "")
	wf.Inject(p.ID, "go")
	wf.Step(context.Background(), p.ID)

	var found bool
	for _, e := range wf.Log.All() {
		if e.Type == EventPromptPressure && e.Data["level"] == "critical" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected EventPromptPressure with level=critical at 96% utilisation")
	}
}

func TestNoPressureUnderThreshold(t *testing.T) {
	gen := &fakeEstimatorGen{
		estimate: 500,
		limit:    1000,
	}

	wf, err := NewWorkflow(gen)
	if err != nil {
		t.Fatal(err)
	}

	p := wf.Spawn("test", "test", nil, "")
	wf.Inject(p.ID, "go")
	wf.Step(context.Background(), p.ID)

	for _, e := range wf.Log.All() {
		if e.Type == EventPromptPressure {
			t.Fatal("should not emit EventPromptPressure at 50% utilisation")
		}
	}
}

func TestRegisterModelLimit(t *testing.T) {
	RegisterModelLimit("custom-model-xyz", 50000)
	got := lookupModelLimit("custom-model-xyz")
	if got != 50000 {
		t.Fatalf("lookupModelLimit = %d, want 50000", got)
	}
}

func TestDefaultEstimateTokens(t *testing.T) {
	ctx := Context{
		Purpose:   "You are a test agent",
		LastInput: "hello world",
	}
	est := defaultEstimateTokens(ctx)
	if est <= 0 {
		t.Fatalf("estimate should be > 0, got %d", est)
	}
	expectedChars := len(ctx.Purpose) + len(ctx.LastInput)
	if est != expectedChars/4 {
		t.Fatalf("estimate = %d, want %d (chars/4)", est, expectedChars/4)
	}
}

type fakeEstimatorGen struct {
	estimate int
	limit    int
}

func (g *fakeEstimatorGen) Generate(ctx Context) Proposal {
	return Proposal{Text: "done"}
}

func (g *fakeEstimatorGen) EstimateTokens(ctx Context) int {
	return g.estimate
}

func (g *fakeEstimatorGen) ModelLimit() int {
	return g.limit
}
