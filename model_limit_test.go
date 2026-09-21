package orchid

import (
	"context"
	"testing"
)

// TestCurrentModelsResolveALimit guards the failure mode that made the
// prompt-pressure events dead code: every entry in the table had aged out, so
// every lookup returned zero, and a zero limit disables the pressure checks
// without reporting anything. A model anyone would actually run must resolve.
func TestCurrentModelsResolveALimit(t *testing.T) {
	for _, model := range []string{
		"claude-opus-5",
		"claude-sonnet-5",
		"claude-haiku-4-5",
		"claude-fable-5-1",
		"anthropic.claude-opus-5",
		"anthropic.claude-sonnet-5",
		"gpt-4o",
	} {
		if limit := lookupModelLimit(model); limit == 0 {
			t.Errorf("lookupModelLimit(%q) = 0; a current model must resolve a context window", model)
		}
	}
}

// TestModelLimitPrefixIsDeterministic pins longest-prefix resolution. Iterating
// the table and taking the first prefix that matches returns a different answer
// from run to run, because Go randomises map iteration order.
func TestModelLimitPrefixIsDeterministic(t *testing.T) {
	RegisterModelLimit("test-model", 1_000)
	RegisterModelLimit("test-model-large", 500_000)
	t.Cleanup(func() {
		delete(modelLimits, "test-model")
		delete(modelLimits, "test-model-large")
	})

	seen := make(map[int]int)
	for i := 0; i < 2_000; i++ {
		seen[lookupModelLimit("test-model-large-v2")]++
	}
	if len(seen) != 1 {
		t.Fatalf("same input resolved to %d different limits: %v", len(seen), seen)
	}
	if got := lookupModelLimit("test-model-large-v2"); got != 500_000 {
		t.Errorf("want the longest matching prefix (500000), got %d", got)
	}
}

// TestContextLimitOverrideBeatsTable pins that a caller can supply a limit for a
// model the table does not know, without waiting on a library release.
func TestContextLimitOverrideBeatsTable(t *testing.T) {
	gen := NewOpenAIGenerator("http://localhost:1",
		OpenAIWithModel("some-local-model"),
		OpenAIWithContextLimit(64_000),
	)
	if lookupModelLimit("some-local-model") != 0 {
		t.Fatal("precondition: the table should not know this model")
	}
	if got := gen.ModelLimit(); got != 64_000 {
		t.Errorf("ModelLimit() = %d, want the configured 64000", got)
	}
}

// TestUnknownModelLimitIsReported pins that an unknown context window is stated
// in the log. Pressure checks are gated on a non-zero limit, so without this
// marker an unmeasured run is indistinguishable from a run with plenty of room.
func TestUnknownModelLimitIsReported(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{Text: "done"}
	})

	wf, err := NewWorkflowWithOptions(gen)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := wf.Run(context.Background(), "p", "task"); err != nil {
		t.Fatal(err)
	}

	var sizeEvents int
	for _, e := range wf.Log.All() {
		if e.Type != EventPromptSize {
			continue
		}
		sizeEvents++
		if e.Data["model_limit"] != "0" {
			t.Fatalf("expected an unknown limit for a generator with no model, got %q", e.Data["model_limit"])
		}
		if e.Data["model_limit_unknown"] != "true" {
			t.Error("EventPromptSize with a zero limit must record model_limit_unknown")
		}
	}
	if sizeEvents == 0 {
		t.Fatal("expected at least one EventPromptSize")
	}
}

// TestKnownModelLimitIsNotMarkedUnknown is the other half: a resolved limit must
// not carry the marker, or the marker means nothing.
func TestKnownModelLimitIsNotMarkedUnknown(t *testing.T) {
	gen := &fixedLimitGenerator{limit: 200_000}

	wf, err := NewWorkflowWithOptions(gen)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := wf.Run(context.Background(), "p", "task"); err != nil {
		t.Fatal(err)
	}

	for _, e := range wf.Log.All() {
		if e.Type != EventPromptSize {
			continue
		}
		if _, marked := e.Data["model_limit_unknown"]; marked {
			t.Error("a resolved limit must not be marked unknown")
		}
		if e.Data["model_limit"] != "200000" {
			t.Errorf("model_limit = %q, want 200000", e.Data["model_limit"])
		}
	}
}

type fixedLimitGenerator struct{ limit int }

func (g *fixedLimitGenerator) Generate(ctx Context) Proposal { return Proposal{Text: "done"} }
func (g *fixedLimitGenerator) Model() string                 { return "test-fixed" }
func (g *fixedLimitGenerator) ModelLimit() int               { return g.limit }

// TestBedrockIDSpellingsResolve covers the cross-region prefixes Bedrock uses,
// which extend the plain "anthropic." ids rather than replacing them.
func TestBedrockIDSpellingsResolve(t *testing.T) {
	RegisterModelLimit("us.anthropic.claude-opus-5", 1_000_000)
	t.Cleanup(func() { delete(modelLimits, "us.anthropic.claude-opus-5") })

	for _, model := range []string{
		"anthropic.claude-opus-5",
		"anthropic.claude-opus-5-v1:0",
		"us.anthropic.claude-opus-5",
	} {
		if limit := lookupModelLimit(model); limit == 0 {
			t.Errorf("lookupModelLimit(%q) = 0", model)
		}
	}

	// A bare family name is shorter than every table entry, so it matches no
	// prefix and must not resolve to some sibling model's window.
	if limit := lookupModelLimit("claude"); limit != 0 {
		t.Errorf("lookupModelLimit(%q) = %d, want 0", "claude", limit)
	}
}
