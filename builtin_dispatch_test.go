package orchid

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type oneShotGen struct {
	effects []ProposedEffect
	done    bool
}

func (g *oneShotGen) Generate(ctx Context) Proposal {
	if g.done {
		return Proposal{Text: "done"}
	}
	g.done = true
	return Proposal{Text: "calling", Effects: g.effects}
}

func TestEveryBuiltinToolDispatches(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		tool string
		args map[string]string
		make func(string) Tool
	}{
		{"read", map[string]string{"path": "sample.txt"}, func(r string) Tool { return ReadTool(r) }},
		{"read_bytes", map[string]string{"path": "sample.txt"}, func(r string) Tool { return ReadBytesTool(r) }},
		{"search", map[string]string{"pattern": "alpha"}, func(r string) Tool { return SearchTool(r) }},
		{"list", map[string]string{}, func(r string) Tool { return ListTool(r) }},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			gen := &oneShotGen{effects: []ProposedEffect{{Tool: tc.tool, Args: tc.args}}}
			wf, err := NewWorkflowWithOptions(gen, WithRootDir(dir), WithTools(tc.make(dir)))
			if err != nil {
				t.Fatalf("constructing workflow: %v", err)
			}

			if _, _, err := wf.Run(context.Background(), "guard", "call the tool"); err != nil {
				t.Fatalf("run: %v", err)
			}

			var observed, rejected int
			for _, e := range wf.Log.All() {
				switch e.Type {
				case EventEffectObserved:
					observed++
				case EventEffectRejected:
					rejected++
					t.Errorf("%s was rejected: %s", tc.tool, e.Data[KeyReason])
				}
			}
			if observed != 1 {
				t.Errorf("want exactly 1 observation for %s, got %d (rejected %d)", tc.tool, observed, rejected)
			}
		})
	}
}

func TestBuiltinToolsAreContextual(t *testing.T) {
	dir := t.TempDir()
	for _, tool := range []Tool{ReadTool(dir), ReadBytesTool(dir), SearchTool(dir), ListTool(dir)} {
		if tool.Handler != nil {
			t.Errorf("%s: expected Handler to be unset", tool.Name)
		}
		if tool.ContextualHandler == nil {
			t.Errorf("%s: expected ContextualHandler to be set", tool.Name)
		}
	}
}
