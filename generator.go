package orchid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
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

var modelLimits = map[string]int{
	"anthropic.claude-3-5-sonnet-20241022-v2:0": 200000,
	"anthropic.claude-3-5-haiku-20241022-v1:0":  200000,
	"anthropic.claude-3-sonnet-20240229-v1:0":   200000,
	"anthropic.claude-3-haiku-20240307-v1:0":    200000,
	"anthropic.claude-v2":                       100000,
	"gpt-4":                                     128000,
	"gpt-4-turbo":                               128000,
	"gpt-4o":                                    128000,
	"gpt-3.5-turbo":                             16385,
}

func RegisterModelLimit(model string, limit int) {
	modelLimits[model] = limit
}

func lookupModelLimit(model string) int {
	if limit, ok := modelLimits[model]; ok {
		return limit
	}
	for prefix, limit := range modelLimits {
		if len(model) > len(prefix) && model[:len(prefix)] == prefix {
			return limit
		}
	}
	return 0
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
