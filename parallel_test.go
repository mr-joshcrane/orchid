package orchid

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestParallelEffects(t *testing.T) {
	var callCount atomic.Int32
	gen := GeneratorFunc(func(ctx Context) Proposal {
		if len(ctx.ToolResults) > 0 {
			return Proposal{Text: "done"}
		}
		return Proposal{
			Text: "reading",
			Effects: []ProposedEffect{
				{Tool: "slow", Args: map[string]string{"n": "1"}},
				{Tool: "slow", Args: map[string]string{"n": "2"}},
				{Tool: "slow", Args: map[string]string{"n": "3"}},
				{Tool: "slow", Args: map[string]string{"n": "4"}},
			},
		}
	})

	slowTool := Tool{
		Name:           "slow",
		Parallelizable: true,
		ContextualHandler: func(hctx HandlerContext, args map[string]string) (string, error) {
			callCount.Add(1)
			time.Sleep(100 * time.Millisecond)
			return "done-" + args["n"], nil
		},
	}

	wf, err := NewWorkflowWithOptions(gen,
		WithTools(slowTool),
		WithEffectConcurrency(4),
	)
	if err != nil {
		t.Fatal(err)
	}

	p := wf.Spawn("test", "", []Capability{"slow"}, "")
	p.Tools = []ToolDefinition{{Name: "slow"}}
	p.View.Filter = func(e Event) bool { return e.ProcessID == p.ID }
	wf.Inject(p.ID, "go")

	start := time.Now()
	_, results, _ := wf.Step(context.Background(), p.ID)
	elapsed := time.Since(start)

	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}
	if callCount.Load() != 4 {
		t.Fatalf("expected 4 calls, got %d", callCount.Load())
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("4x100ms tools should complete in ~100ms with concurrency=4, took %s", elapsed)
	}
}

func TestParallelPreservesOrder(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		if len(ctx.ToolResults) > 0 {
			return Proposal{Text: "done"}
		}
		var effects []ProposedEffect
		for i := 0; i < 8; i++ {
			effects = append(effects, ProposedEffect{
				Tool: "order",
				Args: map[string]string{"n": fmt.Sprintf("%d", i)},
			})
		}
		return Proposal{Text: "working", Effects: effects}
	})

	orderTool := Tool{
		Name:           "order",
		Parallelizable: true,
		ContextualHandler: func(hctx HandlerContext, args map[string]string) (string, error) {
			time.Sleep(time.Duration(10-mustAtoi(args["n"])) * time.Millisecond)
			return "result-" + args["n"], nil
		},
	}

	wf, err := NewWorkflowWithOptions(gen,
		WithTools(orderTool),
		WithEffectConcurrency(4),
	)
	if err != nil {
		t.Fatal(err)
	}

	p := wf.Spawn("test", "", []Capability{"order"}, "")
	p.Tools = []ToolDefinition{{Name: "order"}}
	p.View.Filter = func(e Event) bool { return e.ProcessID == p.ID }
	wf.Inject(p.ID, "go")

	_, results, _ := wf.Step(context.Background(), p.ID)

	for i, r := range results {
		want := fmt.Sprintf("result-%d", i)
		if r.Result != want {
			t.Fatalf("results[%d] = %q, want %q (order not preserved)", i, r.Result, want)
		}
	}
}

func TestSerialToolDrainsParallel(t *testing.T) {
	var order []string
	var orderMu = make(chan struct{}, 1)
	appendOrder := func(s string) {
		orderMu <- struct{}{}
		order = append(order, s)
		<-orderMu
	}

	gen := GeneratorFunc(func(ctx Context) Proposal {
		if len(ctx.ToolResults) > 0 {
			return Proposal{Text: "done"}
		}
		return Proposal{
			Text: "working",
			Effects: []ProposedEffect{
				{Tool: "fast", Args: map[string]string{"n": "1"}},
				{Tool: "fast", Args: map[string]string{"n": "2"}},
				{Tool: "serial", Args: map[string]string{}},
				{Tool: "fast", Args: map[string]string{"n": "3"}},
			},
		}
	})

	fastTool := Tool{
		Name:           "fast",
		Parallelizable: true,
		ContextualHandler: func(hctx HandlerContext, args map[string]string) (string, error) {
			time.Sleep(20 * time.Millisecond)
			appendOrder("fast-" + args["n"])
			return "fast", nil
		},
	}

	serialTool := Tool{
		Name:           "serial",
		Parallelizable: false,
		ContextualHandler: func(hctx HandlerContext, args map[string]string) (string, error) {
			appendOrder("serial")
			return "serial", nil
		},
	}

	wf, err := NewWorkflowWithOptions(gen,
		WithTools(fastTool, serialTool),
		WithEffectConcurrency(4),
	)
	if err != nil {
		t.Fatal(err)
	}

	p := wf.Spawn("test", "", []Capability{"fast", "serial"}, "")
	p.Tools = []ToolDefinition{{Name: "fast"}, {Name: "serial"}}
	p.View.Filter = func(e Event) bool { return e.ProcessID == p.ID }
	wf.Inject(p.ID, "go")

	_, results, _ := wf.Step(context.Background(), p.ID)

	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}
	if results[2].Result != "serial" {
		t.Fatalf("results[2] should be serial tool, got %q", results[2].Result)
	}

	serialIdx := -1
	for i, s := range order {
		if s == "serial" {
			serialIdx = i
			break
		}
	}
	if serialIdx < 2 {
		t.Fatalf("serial tool ran at index %d, should run after fast-1 and fast-2 complete (order: %v)", serialIdx, order)
	}
}

func mustAtoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestFatalEffectStopsStepSerial(t *testing.T) {
	var callCount atomic.Int32

	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text: "working",
			Effects: []ProposedEffect{
				{Tool: "boom", Args: map[string]string{"n": "1"}},
				{Tool: "boom", Args: map[string]string{"n": "2"}},
				{Tool: "boom", Args: map[string]string{"n": "3"}},
			},
		}
	})

	boomTool := Tool{
		Name: "boom",
		ContextualHandler: func(hctx HandlerContext, args map[string]string) (string, error) {
			callCount.Add(1)
			if args["n"] == "1" {
				return "", fmt.Errorf("first failed: %w", ErrEffectFatal)
			}
			return "ok", nil
		},
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(boomTool))
	if err != nil {
		t.Fatal(err)
	}

	p := wf.Spawn("test", "", []Capability{"boom"}, "")
	p.Tools = []ToolDefinition{{Name: "boom"}}
	p.View.Filter = func(e Event) bool { return e.ProcessID == p.ID }
	wf.Inject(p.ID, "go")

	_, _, _ = wf.Step(context.Background(), p.ID)

	if callCount.Load() != 1 {
		t.Fatalf("fatal should short-circuit the loop: expected 1 call, got %d", callCount.Load())
	}
	if p.Err == nil {
		t.Fatal("expected p.Err to be set when a fatal effect occurs")
	}
	if p.State != ProcessFailed {
		t.Fatalf("expected ProcessFailed, got %v", p.State)
	}
}

func TestFatalEffectStopsStepParallel(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text: "working",
			Effects: []ProposedEffect{
				{Tool: "boom", Args: map[string]string{"n": "1"}},
				{Tool: "boom", Args: map[string]string{"n": "2"}},
				{Tool: "boom", Args: map[string]string{"n": "3"}},
				{Tool: "boom", Args: map[string]string{"n": "4"}},
			},
		}
	})

	boomTool := Tool{
		Name:           "boom",
		Parallelizable: true,
		ContextualHandler: func(hctx HandlerContext, args map[string]string) (string, error) {
			if args["n"] == "2" {
				return "", fmt.Errorf("index 2 failed: %w", ErrEffectFatal)
			}
			time.Sleep(20 * time.Millisecond)
			return "ok", nil
		},
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(boomTool), WithEffectConcurrency(4))
	if err != nil {
		t.Fatal(err)
	}

	p := wf.Spawn("test", "", []Capability{"boom"}, "")
	p.Tools = []ToolDefinition{{Name: "boom"}}
	p.View.Filter = func(e Event) bool { return e.ProcessID == p.ID }
	wf.Inject(p.ID, "go")

	_, _, _ = wf.Step(context.Background(), p.ID)

	if p.Err == nil {
		t.Fatal("expected p.Err to be set when a fatal effect occurs in parallel batch")
	}
	if p.State != ProcessFailed {
		t.Fatalf("expected ProcessFailed, got %v", p.State)
	}
}

func TestFatalEffectStopsStepMixedBatches(t *testing.T) {
	var callCount atomic.Int32

	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{
			Text: "working",
			Effects: []ProposedEffect{
				{Tool: "fast", Args: map[string]string{"n": "a"}},
				{Tool: "fast", Args: map[string]string{"n": "b"}},
				{Tool: "serial_boom", Args: map[string]string{}},
				{Tool: "fast", Args: map[string]string{"n": "c"}},
			},
		}
	})

	fastTool := Tool{
		Name:           "fast",
		Parallelizable: true,
		ContextualHandler: func(hctx HandlerContext, args map[string]string) (string, error) {
			callCount.Add(1)
			return "ok", nil
		},
	}

	serialBoomTool := Tool{
		Name: "serial_boom",
		ContextualHandler: func(hctx HandlerContext, args map[string]string) (string, error) {
			return "", fmt.Errorf("serial step failed: %w", ErrEffectFatal)
		},
	}

	wf, err := NewWorkflowWithOptions(gen,
		WithTools(fastTool, serialBoomTool),
		WithEffectConcurrency(4),
	)
	if err != nil {
		t.Fatal(err)
	}

	p := wf.Spawn("test", "", []Capability{"fast", "serial_boom"}, "")
	p.Tools = []ToolDefinition{{Name: "fast"}, {Name: "serial_boom"}}
	p.View.Filter = func(e Event) bool { return e.ProcessID == p.ID }
	wf.Inject(p.ID, "go")

	_, _, _ = wf.Step(context.Background(), p.ID)

	if p.Err == nil {
		t.Fatal("expected p.Err to be set")
	}
	if got := callCount.Load(); got != 2 {
		t.Fatalf("expected 2 fast-tool calls (pre-serial batch only), got %d", got)
	}
}
