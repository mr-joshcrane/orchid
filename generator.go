package orchid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type TokenUsage struct {
	InputTokens  int
	OutputTokens int
}

type Proposal struct {
	Text         string
	Effects      []ProposedEffect
	Err          error
	PromptID     string
	RawMessage   string
	InputTokens  int
	OutputTokens int
}

type ProposedEffect struct {
	Tool      string
	Args      map[string]string
	ToolUseID string
}

type Context struct {
	Ctx     context.Context
	Name    string
	Purpose string
	Events  []Event
	// Messages is the conversation derived from Events — the only history a
	// generator sends. Tool results for dispatched effects are already in it.
	Messages      []Message
	LastInput     string
	ToolResults   []ToolResultEntry
	Step          int
	Tools         []ToolDefinition
	ContextPolicy ContextPolicy
}

type ToolResultEntry struct {
	Tool   string
	Result string
}

type ToolDefinition struct {
	Name        string
	Description string
	Params      map[string]Param
}

type Generator interface {
	Generate(ctx Context) Proposal
}

type ModelIdentifier interface {
	Model() string
}

type TokenEstimator interface {
	EstimateTokens(ctx Context) int
}

type ModelLimiter interface {
	ModelLimit() int
}

// modelLimits maps a model id to its context window in tokens. It is a
// convenience seed, not an authority: a hardcoded table goes stale every time a
// provider ships a model, and a stale entry is invisible because a miss returns
// zero, which silently disables the prompt-pressure events that depend on it.
//
// Prefer telling the generator its own limit — BedrockWithContextLimit or
// OpenAIWithContextLimit — which needs no library release to stay correct. When
// a lookup misses, the EventPromptSize event records model_limit_unknown so the
// gap shows up in the audit log rather than passing for a healthy zero.
//
// Bedrock ids carry an "anthropic." prefix; prefix matching covers the
// "us."/"eu." cross-region spellings and any trailing version suffix.
var modelLimits = map[string]int{
	// Anthropic, first-party ids.
	"claude-fable-5-1":  1_000_000,
	"claude-fable-5":    1_000_000,
	"claude-mythos-5-1": 1_000_000,
	"claude-opus-5":     1_000_000,
	"claude-opus-4-8":   1_000_000,
	"claude-opus-4-7":   1_000_000,
	"claude-opus-4-6":   1_000_000,
	"claude-sonnet-5":   1_000_000,
	"claude-sonnet-4-6": 1_000_000,
	"claude-haiku-4-5":  200_000,

	// Anthropic on Bedrock.
	"anthropic.claude-fable-5-1":  1_000_000,
	"anthropic.claude-fable-5":    1_000_000,
	"anthropic.claude-opus-5":     1_000_000,
	"anthropic.claude-opus-4-8":   1_000_000,
	"anthropic.claude-opus-4-7":   1_000_000,
	"anthropic.claude-opus-4-6":   1_000_000,
	"anthropic.claude-sonnet-5":   1_000_000,
	"anthropic.claude-sonnet-4-6": 1_000_000,
	"anthropic.claude-haiku-4-5":  200_000,

	// OpenAI-compatible endpoints.
	"gpt-4":         128_000,
	"gpt-4-turbo":   128_000,
	"gpt-4o":        128_000,
	"gpt-3.5-turbo": 16_385,
}

// RegisterModelLimit records a context window for a model id, overriding any
// seeded value. Registering a prefix covers every id that extends it.
func RegisterModelLimit(model string, limit int) {
	modelLimits[model] = limit
}

// lookupModelLimit resolves a model's context window, returning 0 when the model
// is unknown. An exact id wins; otherwise the longest matching prefix does.
// Longest-match rather than first-match because map iteration order is random,
// so first-match returns a different limit from run to run whenever two prefixes
// both apply.
func lookupModelLimit(model string) int {
	if limit, ok := modelLimits[model]; ok {
		return limit
	}
	best, bestLen := 0, 0
	for prefix, limit := range modelLimits {
		if len(prefix) <= bestLen || !strings.HasPrefix(model, prefix) {
			continue
		}
		best, bestLen = limit, len(prefix)
	}
	return best
}

func defaultEstimateTokens(ctx Context) int {
	var chars int
	chars += len(ctx.Purpose)
	chars += len(ctx.LastInput)
	for _, e := range ctx.Events {
		for _, v := range e.Data {
			chars += len(v)
		}
	}
	return chars / 4
}

type GeneratorFunc func(Context) Proposal

func (f GeneratorFunc) Generate(ctx Context) Proposal {
	return f(ctx)
}

type promptTrace struct {
	id  string
	dir string
}

func newPromptTrace() promptTrace {
	id := newPromptID()
	dir := filepath.Join(os.TempDir(), "orchid-prompts")
	os.MkdirAll(dir, 0o755)
	return promptTrace{id: id, dir: dir}
}

func (t promptTrace) save(request, response []byte) {
	record := map[string]json.RawMessage{
		"request":  request,
		"response": response,
	}
	data, _ := json.MarshalIndent(record, "", "  ")
	tmpFile := filepath.Join(t.dir, t.id+".tmp")
	finalFile := filepath.Join(t.dir, t.id+".json")
	err := os.WriteFile(tmpFile, data, 0o644)
	if err != nil {
		return
	}
	os.Rename(tmpFile, finalFile)
}

func newPromptID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func paramSchema(params map[string]Param) map[string]any {
	properties := make(map[string]any, len(params))
	var required []string
	for name, p := range params {
		prop := map[string]any{"type": p.Type}
		if p.Description != "" {
			prop["description"] = p.Description
		}
		properties[name] = prop
		if p.Required {
			required = append(required, name)
		}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
