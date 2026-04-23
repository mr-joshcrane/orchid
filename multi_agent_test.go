package orchid_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mr-joshcrane/orchid"
)

func TestMain(m *testing.M) {
	orchid.DefaultLogDir = ""
	os.Exit(m.Run())
}

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
		rt.Step(context.Background(), child.ID)

		text, _, _ := rt.Step(context.Background(), child.ID)

		found := text == "FOUND"
		results = append(results, childResult{file: filename, found: found})
		rt.Join(parent.ID, child.ID, fmt.Sprintf("%s: %s", filename, text), orchid.TokenUsage{})
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
	planText, _, _ := rt.Step(context.Background(), planner.ID)

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
		text, _, _ := rt.Step(context.Background(), child.ID)
		rt.Join(planner.ID, child.ID, text, orchid.TokenUsage{})
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

func echoGenerator() orchid.Generator {
	return orchid.GeneratorFunc(func(ctx orchid.Context) orchid.Proposal {
		return orchid.Proposal{Text: "echo: " + ctx.LastInput}
	})
}

func TestAgent_Fork_VariadicInheritsTools(t *testing.T) {
	handlerCalled := false
	gen := orchid.GeneratorFunc(func(ctx orchid.Context) orchid.Proposal {
		return orchid.Proposal{
			Text:    "reading",
			Effects: []orchid.ProposedEffect{{Tool: "read", Args: map[string]string{"path": "/tmp"}}},
		}
	})

	a := orchid.New(gen)
	a.Can(orchid.Tool{
		Name:        "read",
		Description: "Read a file",
		Handler: func(args map[string]string) (string, error) {
			handlerCalled = true
			return "content", nil
		},
	})
	a.System("test")

	results, err := a.Fork("read /tmp").Join()

	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !handlerCalled {
		t.Fatal("inherited handler was not called")
	}
}

func TestAgent_Fork_JoinCollectsInOrder(t *testing.T) {
	a := orchid.New(echoGenerator())

	results, err := a.Fork("first", "second", "third").Join()

	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for i, want := range []string{"echo: first", "echo: second", "echo: third"} {
		if results[i].Text != want {
			t.Errorf("results[%d].Text = %q, want %q", i, results[i].Text, want)
		}
		if results[i].Index != i {
			t.Errorf("results[%d].Index = %d, want %d", i, results[i].Index, i)
		}
	}
}

func TestAgent_Fork_EmptyReturnsEmpty(t *testing.T) {
	a := orchid.New(echoGenerator())

	results, err := a.Fork().Join()

	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestAgent_Fork_StreamDeliversResults(t *testing.T) {
	a := orchid.New(echoGenerator())

	count := 0
	for r := range a.Fork("a", "b", "c").Stream() {
		if r.Err != nil {
			t.Fatal(r.Err)
		}
		if !strings.HasPrefix(r.Text, "echo: ") {
			t.Errorf("unexpected text: %q", r.Text)
		}
		count++
	}
	if count != 3 {
		t.Fatalf("streamed %d results, want 3", count)
	}
}

func TestForkAgents_DifferentTools(t *testing.T) {
	gen := orchid.GeneratorFunc(func(ctx orchid.Context) orchid.Proposal {
		return orchid.Proposal{
			Text:    "reading",
			Effects: []orchid.ProposedEffect{{Tool: "read", Args: map[string]string{}}},
		}
	})

	agentA := orchid.New(gen)
	agentA.Can(orchid.Tool{
		Name: "read", Description: "read",
		Handler: func(args map[string]string) (string, error) { return "from-A", nil },
	})
	agentA.System("agent A")
	agentA.GiveTask("go")

	agentB := orchid.New(gen)
	agentB.Can(orchid.Tool{
		Name: "read", Description: "read",
		Handler: func(args map[string]string) (string, error) { return "from-B", nil },
	})
	agentB.System("agent B")
	agentB.GiveTask("go")

	results, err := orchid.ForkAgents(agentA, agentB).Join()

	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	resultMap := map[int]string{}
	for _, r := range results {
		resultMap[r.Index] = r.Text
	}

	if resultMap[0] != "reading" || resultMap[1] != "reading" {
		t.Fatalf("unexpected results: %v", resultMap)
	}
}

func TestForkAgents_Empty(t *testing.T) {
	results, err := orchid.ForkAgents().Join()

	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestAgent_Fork_SetMaxSteps(t *testing.T) {
	stepCount := 0
	gen := orchid.GeneratorFunc(func(ctx orchid.Context) orchid.Proposal {
		stepCount++
		return orchid.Proposal{
			Text:    "working",
			Effects: []orchid.ProposedEffect{{Tool: "read", Args: map[string]string{"n": fmt.Sprintf("%d", stepCount)}}},
		}
	})

	a := orchid.New(gen)
	a.Can(orchid.Tool{
		Name: "read", Description: "read",
		Handler: func(args map[string]string) (string, error) { return "data", nil },
	})

	g := a.Fork("go")
	g.SetMaxSteps(2)
	g.Join()

	if stepCount != 2 {
		t.Fatalf("stepCount = %d, want 2", stepCount)
	}
}

func TestAgent_Fork_OnStep(t *testing.T) {
	callbackCalled := false
	stepCount := 0
	gen := orchid.GeneratorFunc(func(ctx orchid.Context) orchid.Proposal {
		stepCount++
		if stepCount == 1 {
			return orchid.Proposal{
				Text:    "working",
				Effects: []orchid.ProposedEffect{{Tool: "read", Args: map[string]string{}}},
			}
		}
		return orchid.Proposal{Text: "done"}
	})

	a := orchid.New(gen)
	a.Can(orchid.Tool{
		Name: "read", Description: "read",
		Handler: func(args map[string]string) (string, error) { return "data", nil },
	})

	g := a.Fork("go")
	g.OnStep(func(step int, text string, results []orchid.EffectResult) (string, bool) {
		callbackCalled = true
		return "", false
	})

	results, _ := g.Join()

	if !callbackCalled {
		t.Fatal("OnStep callback was not called")
	}
	if results[0].Text != "done" {
		t.Fatalf("text = %q, want %q", results[0].Text, "done")
	}
}

func TestBuildProcessTree_Structure(t *testing.T) {
	gen := orchid.GeneratorFunc(func(ctx orchid.Context) orchid.Proposal {
		return orchid.Proposal{Text: "echo: " + ctx.LastInput, InputTokens: 50, OutputTokens: 10}
	})

	a := orchid.New(gen)
	a.System("test")
	results, _ := a.Fork("task A", "task B", "task C").Join()
	_ = results

	roots := orchid.BuildProcessTree(a.Log())

	if len(roots) != 1 {
		t.Fatalf("expected 1 root, got %d", len(roots))
	}
	root := roots[0]
	if root.Name != "coordinator" {
		t.Fatalf("root.Name = %q, want %q", root.Name, "coordinator")
	}
	if len(root.Children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(root.Children))
	}
}

func TestBuildProcessTree_TokenRollup(t *testing.T) {
	gen := orchid.GeneratorFunc(func(ctx orchid.Context) orchid.Proposal {
		return orchid.Proposal{Text: "ok", InputTokens: 100, OutputTokens: 20}
	})

	a := orchid.New(gen)
	a.Fork("a", "b").Join()

	roots := orchid.BuildProcessTree(a.Log())
	root := roots[0]

	if root.OwnUsage.InputTokens != 0 {
		t.Fatalf("coordinator own tokens = %d, want 0", root.OwnUsage.InputTokens)
	}

	if root.TotalUsage.InputTokens != 200 {
		t.Fatalf("coordinator total input = %d, want 200 (2 children × 100)", root.TotalUsage.InputTokens)
	}
	if root.TotalUsage.OutputTokens != 40 {
		t.Fatalf("coordinator total output = %d, want 40", root.TotalUsage.OutputTokens)
	}

	for _, child := range root.Children {
		if child.OwnUsage.InputTokens != 100 {
			t.Fatalf("child own input = %d, want 100", child.OwnUsage.InputTokens)
		}
	}
}

func TestPrintTree_Output(t *testing.T) {
	gen := orchid.GeneratorFunc(func(ctx orchid.Context) orchid.Proposal {
		return orchid.Proposal{Text: "ok", InputTokens: 100, OutputTokens: 20}
	})

	a := orchid.New(gen)
	a.Fork("a", "b").Join()

	roots := orchid.BuildProcessTree(a.Log())

	var buf bytes.Buffer
	orchid.PrintTree(&buf, roots)

	output := buf.String()
	if !strings.Contains(output, "coordinator") {
		t.Fatalf("output missing coordinator:\n%s", output)
	}
	if !strings.Contains(output, "worker-0") {
		t.Fatalf("output missing worker-0:\n%s", output)
	}
	if !strings.Contains(output, "worker-1") {
		t.Fatalf("output missing worker-1:\n%s", output)
	}
	if !strings.Contains(output, "total=200/40") {
		t.Fatalf("output missing rollup total:\n%s", output)
	}
	t.Logf("PrintTree output:\n%s", output)
}
