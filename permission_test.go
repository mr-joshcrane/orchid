package orchid

import (
	"context"
	"testing"
)

func TestPermissionFromTool(t *testing.T) {
	p := PermissionFromTool("read")
	if p != "tool:read" {
		t.Fatalf("PermissionFromTool(read) = %q, want %q", p, "tool:read")
	}
}

func TestPermissionBundle(t *testing.T) {
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

	wf, err := NewWorkflowWithOptions(gen, WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}

	p := wf.Spawn("reader", "reads files", nil, "")
	p.Permissions[PermissionReadOnlyFS] = true

	p.Tools = []ToolDefinition{{Name: "read"}}
	p.View.Filter = func(e Event) bool { return e.ProcessID == p.ID }
	wf.Inject(p.ID, "go")

	_, results, _, _ := wf.Step(context.Background(), p.ID)
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if !results[0].Admitted {
		t.Fatalf("read should be admitted via PermissionReadOnlyFS bundle, error: %s", results[0].Error)
	}
}

func TestPermissionBundleCustom(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		if len(ctx.ToolResults) > 0 {
			return Proposal{Text: "done"}
		}
		return Proposal{
			Text:    "deploying",
			Effects: []ProposedEffect{{Tool: "deploy", Args: map[string]string{}}},
		}
	})

	deployTool := Tool{
		Name:    "deploy",
		Handler: func(args map[string]string) (string, error) { return "deployed", nil },
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(deployTool))
	if err != nil {
		t.Fatal(err)
	}
	wf.RegisterPermissionBundle("ops:deploy", "deploy", "rollback")

	p := wf.Spawn("deployer", "deploys", nil, "")
	p.Permissions[Permission("ops:deploy")] = true
	p.Tools = []ToolDefinition{{Name: "deploy"}}
	p.View.Filter = func(e Event) bool { return e.ProcessID == p.ID }
	wf.Inject(p.ID, "go")

	_, results, _, _ := wf.Step(context.Background(), p.ID)
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if !results[0].Admitted {
		t.Fatalf("deploy should be admitted via custom bundle, error: %s", results[0].Error)
	}
}

func TestNoGrantIsRefused(t *testing.T) {
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

	wf, err := NewWorkflowWithOptions(gen, WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}

	p := wf.Spawn("reader", "reads files", nil, "")
	p.Permissions = make(map[Permission]bool)
	p.Tools = []ToolDefinition{{Name: "read"}}
	p.View.Filter = func(e Event) bool { return e.ProcessID == p.ID }
	wf.Inject(p.ID, "go")

	_, results, _, _ := wf.Step(context.Background(), p.ID)
	if len(results) == 0 {
		t.Fatal("expected a result for the proposed call")
	}
	if results[0].Admitted {
		t.Fatal("call was admitted with an empty permission set")
	}
	if results[0].Error == "" {
		t.Fatal("refusal carried no reason for the model to read")
	}

	for _, e := range wf.Log.All() {
		if e.Type == EventEffectObserved {
			t.Fatal("a refused call must not record an observation")
		}
	}
}

func TestPermissionDenied(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text:    "want to write",
			Effects: []ProposedEffect{{Tool: "write", Args: map[string]string{}}},
		}
	})

	wf, err := NewWorkflowWithOptions(gen)
	if err != nil {
		t.Fatal(err)
	}

	p := wf.Spawn("reader", "", nil, "")
	p.Permissions[PermissionReadOnlyFS] = true
	wf.Inject(p.ID, "go")

	_, results, _, _ := wf.Step(context.Background(), p.ID)
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if results[0].Admitted {
		t.Fatal("write should be denied — not in PermissionReadOnlyFS bundle")
	}
}

func TestSpawnAutoPopulatesPermissions(t *testing.T) {
	wf, err := NewWorkflowWithOptions(nopGenerator())
	if err != nil {
		t.Fatal(err)
	}

	p := wf.Spawn("worker", "", []Capability{"read", "bash"}, "")

	if !p.HasPermission(PermissionFromTool("read")) {
		t.Fatal("should have tool:read permission")
	}
	if !p.HasPermission(PermissionFromTool("bash")) {
		t.Fatal("should have tool:bash permission")
	}
	if p.HasPermission(PermissionFromTool("write")) {
		t.Fatal("should not have tool:write permission")
	}
}
