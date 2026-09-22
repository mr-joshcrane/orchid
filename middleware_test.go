package orchid

import (
	"context"
	"strings"
	"testing"
)

func TestWorkflow_Use_AppliesMiddleware(t *testing.T) {
	var order []string
	mw := func(tag string) ToolMiddleware {
		return func(next ContextualEffectHandler) ContextualEffectHandler {
			return func(hctx HandlerContext, args map[string]string) (string, error) {
				order = append(order, tag+"-before")
				result, err := next(hctx, args)
				order = append(order, tag+"-after")
				return result, err
			}
		}
	}

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
	wf.Use(mw("A"), mw("B"))

	text, _, runErr := wf.Run(context.Background(), "test", "go")
	if runErr != nil {
		t.Fatal(runErr)
	}
	if text != "done" {
		t.Fatalf("text = %q, want %q", text, "done")
	}

	want := "A-before,B-before,B-after,A-after"
	got := strings.Join(order, ",")
	if got != want {
		t.Fatalf("middleware order = %q, want %q", got, want)
	}
}

func TestWorkflow_Use_PanicsAfterRun(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{Text: "done"}
	})

	wf, err := NewWorkflowWithOptions(gen)
	if err != nil {
		t.Fatal(err)
	}
	wf.Run(context.Background(), "test", "go")

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when calling Use after Run")
		}
	}()
	wf.Use(func(next ContextualEffectHandler) ContextualEffectHandler { return next })
}

func TestTool_With_PerToolMiddleware(t *testing.T) {
	var order []string
	mw := func(tag string) ToolMiddleware {
		return func(next ContextualEffectHandler) ContextualEffectHandler {
			return func(hctx HandlerContext, args map[string]string) (string, error) {
				order = append(order, tag+"-before")
				result, err := next(hctx, args)
				order = append(order, tag+"-after")
				return result, err
			}
		}
	}

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
	readTool = readTool.With(mw("per-tool"))

	wf, err := NewWorkflowWithOptions(gen, WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}
	wf.Use(mw("global"))

	text, _, runErr := wf.Run(context.Background(), "test", "go")
	if runErr != nil {
		t.Fatal(runErr)
	}
	if text != "done" {
		t.Fatalf("text = %q, want %q", text, "done")
	}

	want := "global-before,per-tool-before,per-tool-after,global-after"
	got := strings.Join(order, ",")
	if got != want {
		t.Fatalf("middleware order = %q, want %q (per-tool should be inside global)", got, want)
	}
}

func TestTool_With_ReturnsNewTool(t *testing.T) {
	original := Tool{
		Name:    "read",
		Handler: func(args map[string]string) (string, error) { return "original", nil },
	}

	noop := func(next ContextualEffectHandler) ContextualEffectHandler { return next }
	wrapped := original.With(noop)

	if wrapped.Handler != nil {
		t.Fatal("With should nil out Handler and move to ContextualHandler")
	}
	if wrapped.ContextualHandler == nil {
		t.Fatal("With should set ContextualHandler")
	}
	if original.Handler == nil {
		t.Fatal("original tool's Handler should be unchanged")
	}
}

func TestWorkflow_Use_LegacyHandlerGetsMiddleware(t *testing.T) {
	called := false
	mw := func(next ContextualEffectHandler) ContextualEffectHandler {
		return func(hctx HandlerContext, args map[string]string) (string, error) {
			called = true
			return next(hctx, args)
		}
	}

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
	wf.Use(mw)
	wf.Run(context.Background(), "test", "go")

	if !called {
		t.Fatal("middleware should be called even for legacy-registered tools")
	}
}

func TestMiddleware_HandlerContext_ToolNamePopulated(t *testing.T) {
	var gotToolName string
	mw := func(next ContextualEffectHandler) ContextualEffectHandler {
		return func(hctx HandlerContext, args map[string]string) (string, error) {
			gotToolName = hctx.ToolName
			return next(hctx, args)
		}
	}

	gen := GeneratorFunc(func(ctx Context) Proposal {
		if len(ctx.ToolResults) > 0 {
			return Proposal{Text: "done"}
		}
		return Proposal{
			Text:    "reading",
			Effects: []ProposedEffect{{Tool: "myread", Args: map[string]string{}}},
		}
	})

	readTool := Tool{
		Name:    "myread",
		Handler: func(args map[string]string) (string, error) { return "data", nil },
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(readTool))
	if err != nil {
		t.Fatal(err)
	}
	wf.Use(mw)
	wf.Run(context.Background(), "test", "go")

	if gotToolName != "myread" {
		t.Fatalf("ToolName = %q, want %q", gotToolName, "myread")
	}
}
