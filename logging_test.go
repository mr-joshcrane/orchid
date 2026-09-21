package orchid

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithLogDirCreatesFiles(t *testing.T) {
	logDir := filepath.Join(t.TempDir(), "logs")

	gen := GeneratorFunc(func(ctx Context) Proposal {
		if len(ctx.ToolResults) > 0 {
			return Proposal{Text: "done"}
		}
		return Proposal{
			Text:    "reading",
			Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{}}},
		}
	})

	readTool := Tool{
		Name:    "read",
		Handler: func(args map[string]string) (string, error) { return "data", nil },
	}

	wf, err := NewWorkflowWithOptions(gen, WithLogDir(logDir), WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}

	_, _, runErr := wf.Run(context.Background(), "test", "go")
	if runErr != nil {
		t.Fatal(runErr)
	}

	closeErr := wf.Close()
	if closeErr != nil {
		t.Fatal(closeErr)
	}

	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatal(err)
	}

	var hasJSONL, hasAudit, hasTree bool
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, "-events.jsonl") {
			hasJSONL = true
		}
		if strings.HasSuffix(name, "-audit.txt") {
			hasAudit = true
		}
		if strings.HasSuffix(name, "-tree.txt") {
			hasTree = true
		}
	}

	if !hasJSONL {
		t.Fatal("missing events.jsonl file")
	}
	if !hasAudit {
		t.Fatal("missing audit.txt file")
	}
	if !hasTree {
		t.Fatal("missing tree.txt file")
	}
}

func TestCloseFlushesAudit(t *testing.T) {
	logDir := t.TempDir()

	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{Text: "hello", InputTokens: 50, OutputTokens: 10}
	})

	wf, err := NewWorkflowWithOptions(gen, WithLogDir(logDir))
	if err != nil {
		t.Fatal(err)
	}

	p := wf.Spawn("test", "test", nil, "")
	wf.Inject(p.ID, "go")
	wf.Step(context.Background(), p.ID)

	if err := wf.Close(); err != nil {
		t.Fatal(err)
	}

	entries, _ := os.ReadDir(logDir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), "-audit.txt") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(logDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if len(data) == 0 {
			t.Fatal("audit.txt should not be empty")
		}
		if !strings.Contains(string(data), "ProcessSpawned") {
			t.Fatal("audit.txt should contain ProcessSpawned event")
		}
		return
	}
	t.Fatal("no audit.txt found")
}

func TestCloseWithoutLogDirIsNoop(t *testing.T) {
	wf, err := NewWorkflowWithOptions(nopGenerator())
	if err != nil {
		t.Fatal(err)
	}
	if err := wf.Close(); err != nil {
		t.Fatalf("Close on workflow without WithLogDir should be nil, got %v", err)
	}
}

func TestMustRunAlwaysCloses(t *testing.T) {
	logDir := filepath.Join(t.TempDir(), "logs")

	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{Text: "done"}
	})

	text, _, err := MustRun(context.Background(), gen, "test", "go", WithLogDir(logDir))
	if err != nil {
		t.Fatal(err)
	}
	if text != "done" {
		t.Fatalf("text = %q, want %q", text, "done")
	}

	entries, _ := os.ReadDir(logDir)
	var hasAudit bool
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-audit.txt") {
			hasAudit = true
		}
	}
	if !hasAudit {
		t.Fatal("MustRun should have closed and created audit.txt")
	}
}

// TestMustRunPropagatesCloseError pins that a failure inside Close reaches the
// caller. Assigning to err from a deferred function only affects the result when
// the returns are named; with unnamed returns the assignment targets a dead
// local and a failed audit write is silently discarded.
//
// Construction has to succeed for this to test Close rather than setup, so the
// log directory is removed by the generator, mid-run.
func TestMustRunPropagatesCloseError(t *testing.T) {
	logDir := filepath.Join(t.TempDir(), "logs")

	gen := GeneratorFunc(func(ctx Context) Proposal {
		// WithLogDir has already created logDir and opened the jsonl sink inside
		// it. Removing it now leaves Close unable to write the audit file.
		_ = os.RemoveAll(logDir)
		return Proposal{Text: "done"}
	})

	_, _, err := MustRun(context.Background(), gen, "test", "go", WithLogDir(logDir))
	if err == nil {
		t.Fatal("expected the Close failure to propagate through MustRun")
	}
}

func TestMustRunReturnsConstructionError(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{Text: "done"}
	})

	_, _, err := MustRun(context.Background(), gen, "test", "go", WithRootDir("/nonexistent/xyz"))
	if err == nil {
		t.Fatal("expected error from WithRootDir with bad path")
	}
}
