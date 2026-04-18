package orchid

// Proposal is what a Generator returns — text output plus effect intents.
type Proposal struct {
	Text    string
	Effects []ProposedEffect
}

// ProposedEffect is a tool call the generator wants to make.
type ProposedEffect struct {
	Tool string
	Args map[string]string
}

// Context is the structured state visible to a generator.
type Context struct {
	Spec         ProcessSpec
	Events       []Event
	LastInput    string
	ToolResults  []ToolResultEntry
	ChildResults []ChildResultEntry
	Step         int
	Tools        []ToolDefinition
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
