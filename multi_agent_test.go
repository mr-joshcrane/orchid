package orchid_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/mr-joshcrane/orchid"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestWorkflow_Run_FileSearch(t *testing.T) {
	files := map[string]string{
		"file_00.txt": "the quick brown fox",
		"file_01.txt": "jumps over the lazy dog",
		"file_02.txt": "hello world",
		"file_03.txt": "foo bar baz",
		"file_04.txt": "the quick brown fox jumps",
		"file_05.txt": "nothing interesting here",
		"file_06.txt": "hello again my friend",
		"file_07.txt": "some random content",
		"file_08.txt": "the fox is quick and brown",
		"file_09.txt": "goodbye world",
	}

	searchTerm := "fox"

	expected := map[string]bool{}
	for name, content := range files {
		expected[name] = strings.Contains(content, searchTerm)
	}

	gen := &fileSearchGenerator{searchTerm: searchTerm}
	readTool := orchid.Tool{
		Name:        "read",
		Description: "Read a file",
		Handler: func(args map[string]string) (string, error) {
			content, ok := files[args["path"]]
			if !ok {
				return "", fmt.Errorf("file not found: %s", args["path"])
			}
			return content, nil
		},
	}

	wf, err := orchid.NewWorkflowWithOptions(gen, orchid.WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		file  string
		found bool
	}

	filenames := sortedKeys(files)
	results := make([]result, len(filenames))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i, filename := range filenames {
		wg.Add(1)
		go func() {
			defer wg.Done()
			text, _, err := wf.Run(context.Background(), "You check if a file contains a search term.", fmt.Sprintf("check %s for %s", filename, searchTerm))
			if err != nil {
				t.Errorf("Run failed for %s: %v", filename, err)
				return
			}
			mu.Lock()
			results[i] = result{file: filename, found: text == "FOUND"}
			mu.Unlock()
		}()
	}
	wg.Wait()

	for _, r := range results {
		if r.found != expected[r.file] {
			t.Errorf("file %s: got found=%v, want found=%v", r.file, r.found, expected[r.file])
		}
	}
}

type fileSearchGenerator struct {
	searchTerm string
}

func (g *fileSearchGenerator) Generate(ctx orchid.Context) orchid.Proposal {
	if len(ctx.ToolResults) > 0 {
		for _, tr := range ctx.ToolResults {
			if strings.Contains(tr.Result, g.searchTerm) {
				return orchid.Proposal{Text: "FOUND"}
			}
		}
		return orchid.Proposal{Text: "NOT_FOUND"}
	}

	if strings.HasPrefix(ctx.LastInput, "check ") {
		parts := strings.SplitN(ctx.LastInput, " for ", 2)
		filename := strings.TrimPrefix(parts[0], "check ")
		return orchid.Proposal{
			Text: fmt.Sprintf("Reading %s", filename),
			Effects: []orchid.ProposedEffect{
				{Tool: "read", Args: map[string]string{"path": filename}},
			},
		}
	}

	return orchid.Proposal{Text: "[unexpected state]"}
}

func TestWorkflow_Run_StoreImplementation(t *testing.T) {
	gen := &storeGenerator{}

	wf, err := orchid.NewWorkflowWithOptions(gen)
	if err != nil {
		t.Fatal(err)
	}
	planText, _, wfErr := wf.Run(context.Background(), "You design Go interfaces.", "design a key-value store")
	if wfErr != nil {
		t.Fatal(wfErr)
	}

	functions := strings.Split(planText, ",")
	if len(functions) != 3 {
		t.Fatalf("expected 3 functions, got %d: %v", len(functions), functions)
	}

	var implementations []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, fn := range functions {
		fn = strings.TrimSpace(fn)
		wg.Add(1)
		go func() {
			defer wg.Done()
			text, _, err := wf.Run(context.Background(), "You implement Go functions.", fmt.Sprintf("implement %s", fn))
			if err != nil {
				t.Errorf("Run failed for %s: %v", fn, err)
				return
			}
			mu.Lock()
			implementations = append(implementations, text)
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(implementations) != 3 {
		t.Fatalf("expected 3 implementations, got %d", len(implementations))
	}
	for i, impl := range implementations {
		if !strings.Contains(impl, "func") {
			t.Errorf("implementation %d doesn't look like Go code: %s", i, impl)
		}
	}
}

type storeGenerator struct{}

func (g *storeGenerator) Generate(ctx orchid.Context) orchid.Proposal {
	input := ctx.LastInput

	if input == "design a key-value store" {
		return orchid.Proposal{Text: "Get,Set,Delete"}
	}

	if strings.HasPrefix(input, "implement ") {
		fn := strings.TrimPrefix(input, "implement ")
		switch fn {
		case "Get":
			return orchid.Proposal{Text: "func (s *Store) Get(key string) (string, bool) {\n\tv, ok := s.data[key]\n\treturn v, ok\n}"}
		case "Set":
			return orchid.Proposal{Text: "func (s *Store) Set(key, value string) {\n\ts.data[key] = value\n}"}
		case "Delete":
			return orchid.Proposal{Text: "func (s *Store) Delete(key string) {\n\tdelete(s.data, key)\n}"}
		}
	}

	return orchid.Proposal{Text: "[unexpected: " + input + "]"}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

func TestWorkflow_Run_WithTools(t *testing.T) {
	handlerCalled := false
	gen := orchid.GeneratorFunc(func(ctx orchid.Context) orchid.Proposal {
		if len(ctx.ToolResults) > 0 {
			return orchid.Proposal{Text: "got: " + ctx.ToolResults[0].Result}
		}
		return orchid.Proposal{
			Text:    "reading",
			Effects: []orchid.ProposedEffect{{Tool: "read", Args: map[string]string{"path": "/tmp"}}},
		}
	})

	readTool := orchid.Tool{
		Name:        "read",
		Description: "Read a file",
		Handler: func(args map[string]string) (string, error) {
			handlerCalled = true
			return "content", nil
		},
	}

	wf, err := orchid.NewWorkflowWithOptions(gen, orchid.WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}
	text, _, runErr := wf.Run(context.Background(), "test", "read /tmp")

	if runErr != nil {
		t.Fatal(runErr)
	}
	if !handlerCalled {
		t.Fatal("handler was not called")
	}
	if text != "got: content" {
		t.Fatalf("text = %q, want %q", text, "got: content")
	}
}

func TestBuildProcessTree_Structure(t *testing.T) {
	gen := orchid.GeneratorFunc(func(ctx orchid.Context) orchid.Proposal {
		return orchid.Proposal{Text: "echo: " + ctx.LastInput, InputTokens: 50, OutputTokens: 10}
	})

	wf, err := orchid.NewWorkflowWithOptions(gen)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	tasks := []string{"task A", "task B", "task C"}
	for _, task := range tasks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wf.Run(context.Background(), "test", task)
		}()
	}
	wg.Wait()

	roots := orchid.BuildProcessTree(wf.Log.All())

	if len(roots) != 3 {
		t.Fatalf("expected 3 roots, got %d", len(roots))
	}
}

func TestPrintTree_Output(t *testing.T) {
	gen := orchid.GeneratorFunc(func(ctx orchid.Context) orchid.Proposal {
		return orchid.Proposal{Text: "ok", InputTokens: 100, OutputTokens: 20}
	})

	wf, err := orchid.NewWorkflowWithOptions(gen)
	if err != nil {
		t.Fatal(err)
	}
	wf.Run(context.Background(), "worker", "a")
	wf.Run(context.Background(), "worker", "b")

	roots := orchid.BuildProcessTree(wf.Log.All())

	var buf bytes.Buffer
	orchid.PrintTree(&buf, roots)

	output := buf.String()
	if !strings.Contains(output, "worker") {
		t.Fatalf("output missing worker:\n%s", output)
	}
	t.Logf("PrintTree output:\n%s", output)
}
