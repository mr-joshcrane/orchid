package orchid_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mr-joshcrane/orchid"
)

func TestMultiAgentFileSearch(t *testing.T) {
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
	rt := orchid.NewRuntime(gen)

	rt.Effects.RegisterHandler("read", func(args map[string]string) (string, error) {
		content, ok := files[args["path"]]
		if !ok {
			return "", fmt.Errorf("file not found: %s", args["path"])
		}
		return content, nil
	})

	parent := rt.Spawn(
		orchid.ProcessSpec{Name: "coordinator", SystemPrompt: "You coordinate file searches."},
		[]orchid.Capability{orchid.CapRead},
		"",
	)

	type childResult struct {
		file  string
		found bool
	}
	var results []childResult

	filenames := sortedKeys(files)

	for _, filename := range filenames {
		child := rt.Fork(parent.ID, orchid.ProcessSpec{
			Name:         fmt.Sprintf("checker-%s", filename),
			SystemPrompt: "You check if a file contains a search term.",
		}, []orchid.Capability{orchid.CapRead})

		child.View.Filter = func(e orchid.Event) bool {
			return e.ProcessID == child.ID
		}

		rt.Inject(child.ID, fmt.Sprintf("check %s for %s", filename, searchTerm))
		rt.Step(child.ID)

		text, _, _ := rt.Step(child.ID)

		found := text == "FOUND"
		results = append(results, childResult{file: filename, found: found})
		rt.Join(parent.ID, child.ID, fmt.Sprintf("%s: %s", filename, text))
	}

	for _, r := range results {
		if r.found != expected[r.file] {
			t.Errorf("file %s: got found=%v, want found=%v", r.file, r.found, expected[r.file])
		}
	}

	allEvents := rt.Log.All()
	forkCount := 0
	joinCount := 0
	for _, e := range allEvents {
		if e.Type == orchid.EventFork {
			forkCount++
		}
		if e.Type == orchid.EventJoin {
			joinCount++
		}
	}
	if forkCount != 10 {
		t.Errorf("expected 10 forks, got %d", forkCount)
	}
	if joinCount != 10 {
		t.Errorf("expected 10 joins, got %d", joinCount)
	}

	parentCtx := orchid.BuildContext(parent)
	if len(parentCtx.ChildResults) != 10 {
		t.Errorf("expected 10 child results in parent context, got %d", len(parentCtx.ChildResults))
	}

	t.Logf("Search term: %q", searchTerm)
	for _, r := range results {
		t.Logf("  %s: found=%v (expected=%v)", r.file, r.found, expected[r.file])
	}
	t.Logf("Total events: %d", len(allEvents))
}

type fileSearchGenerator struct {
	searchTerm string
}

func (g *fileSearchGenerator) Generate(ctx orchid.Context) orchid.Proposal {
	// got tool results back — report
	if len(ctx.ToolResults) > 0 {
		for _, tr := range ctx.ToolResults {
			if strings.Contains(tr.Result, g.searchTerm) {
				return orchid.Proposal{Text: "FOUND"}
			}
		}
		return orchid.Proposal{Text: "NOT_FOUND"}
	}

	// got a "check <file>" instruction — read the file
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

func TestMultiAgentStoreImplementation(t *testing.T) {
	gen := &storeGenerator{}
	rt := orchid.NewRuntime(gen)

	// given: a planner that decides what functions we need
	planner := rt.Spawn(
		orchid.ProcessSpec{Name: "planner", SystemPrompt: "You design Go interfaces."},
		nil, "",
	)
	rt.Inject(planner.ID, "design a key-value store")
	planText, _, _ := rt.Step(planner.ID)

	functions := strings.Split(planText, ",")
	if len(functions) != 3 {
		t.Fatalf("expected 3 functions, got %d: %v", len(functions), functions)
	}

	// when: we fork a worker per function and join
	var implementations []string
	for i, fn := range functions {
		fn = strings.TrimSpace(fn)
		child := rt.Fork(planner.ID, orchid.ProcessSpec{
			Name:         fmt.Sprintf("impl-%d", i),
			SystemPrompt: "You implement Go functions.",
		}, nil)
		child.View.Filter = func(e orchid.Event) bool {
			return e.ProcessID == child.ID
		}

		rt.Inject(child.ID, fmt.Sprintf("implement %s", fn))
		text, _, _ := rt.Step(child.ID)
		rt.Join(planner.ID, child.ID, text)
		implementations = append(implementations, text)
	}

	// then: we got an implementation for each function
	if len(implementations) != 3 {
		t.Fatalf("expected 3 implementations, got %d", len(implementations))
	}
	for i, impl := range implementations {
		if !strings.Contains(impl, "func") {
			t.Errorf("implementation %d doesn't look like Go code: %s", i, impl)
		}
	}

	// then: the planner's context has all child results
	plannerCtx := orchid.BuildContext(planner)
	if len(plannerCtx.ChildResults) != 3 {
		t.Errorf("expected 3 child results, got %d", len(plannerCtx.ChildResults))
	}

	// then: audit log shows fork/join structure
	allEvents := rt.Log.All()
	forks, joins := 0, 0
	for _, e := range allEvents {
		if e.Type == orchid.EventFork {
			forks++
		}
		if e.Type == orchid.EventJoin {
			joins++
		}
	}
	if forks != 3 {
		t.Errorf("expected 3 forks, got %d", forks)
	}
	if joins != 3 {
		t.Errorf("expected 3 joins, got %d", joins)
	}

	t.Logf("Plan: %s", planText)
	for i, impl := range implementations {
		t.Logf("Implementation %d:\n%s", i, impl)
	}
	t.Logf("Total events: %d", len(allEvents))
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
