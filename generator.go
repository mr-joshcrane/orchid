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
	History      any
	InputTokens  int
	OutputTokens int
}

type ProposedEffect struct {
	Tool      string
	Args      map[string]string
	ToolUseID string
}

type Context struct {
	Ctx            context.Context
	Spec           ProcessSpec
	Events         []Event
	LastInput      string
	ToolResults    []ToolResultEntry
	ChildResults   []ChildResultEntry
	Step           int
	Tools          []ToolDefinition
	History        any
	PendingResults []EffectResult
}

type ToolResultEntry struct {
	Tool   string
	Result string
}

type ChildResultEntry struct {
	ChildID string
	Result  string
}

type ToolDefinition struct {
	Name        string
	Description string
}

type Generator interface {
	Generate(ctx Context) Proposal
}

type ModelIdentifier interface {
	Model() string
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
