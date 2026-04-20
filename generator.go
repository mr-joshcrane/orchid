package orchid

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// Proposal is what a Generator returns — text output plus effect intents.
type Proposal struct {
	Text       string
	Effects    []ProposedEffect
	PromptID   string
	RawMessage string
	History    any
}

// ProposedEffect is a tool call the generator wants to make.
type ProposedEffect struct {
	Tool      string
	Args      map[string]string
	ToolUseID string
}

// Context is the structured state visible to a generator.
type Context struct {
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

// ToolResultEntry pairs a tool name with its result.
type ToolResultEntry struct {
	Tool   string
	Result string
}

// ChildResultEntry pairs a child ID with its joined result.
type ChildResultEntry struct {
	ChildID string
	Result  string
}

type ToolDefinition struct {
	Name        string
	Description string
}

// Generator takes structured context and produces a proposal.
type Generator interface {
	Generate(ctx Context) Proposal
}

// GeneratorFunc adapts a plain function into a Generator.
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
