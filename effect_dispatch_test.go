package orchid

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
)

// scriptedGen replays one batch of proposed calls per step.
type scriptedGen struct {
	steps [][]ProposedEffect
	i     int
}

func (g *scriptedGen) Generate(ctx Context) Proposal {
	if g.i >= len(g.steps) {
		return Proposal{Text: "done"}
	}
	step := g.steps[g.i]
	g.i++
	return Proposal{Text: fmt.Sprintf("step %d", g.i), Effects: step}
}

func observedResults(events []Event, tool string) []string {
	var out []string
	for _, e := range events {
		if e.Type == EventEffectObserved && e.Data[KeyTool] == tool {
			out = append(out, e.Data[KeyResult])
		}
	}
	return out
}

// TestEffectsAreNotMemoized pins that an identical call runs again rather than
// replaying an earlier result. Memoizing by tool and arguments treats effects as
// pure functions of their arguments, which makes a read after a write return the
// pre-write value and makes the event log assert an observation that never
// happened.
func TestEffectsAreNotMemoized(t *testing.T) {
	state := map[string]string{"f": "v1"}
	read := Tool{
		Name:        "read",
		Description: "read a key",
		Params:      map[string]Param{"p": {Type: "string", Required: true}},
		Handler:     func(a map[string]string) (string, error) { return state[a["p"]], nil },
	}
	write := Tool{
		Name:        "write",
		Description: "write a key",
		Params: map[string]Param{
			"p": {Type: "string", Required: true},
			"v": {Type: "string", Required: true},
		},
		Handler: func(a map[string]string) (string, error) {
			state[a["p"]] = a["v"]
			return "ok", nil
		},
	}

	gen := &scriptedGen{steps: [][]ProposedEffect{
		{{Tool: "read", Args: map[string]string{"p": "f"}}},
		{{Tool: "write", Args: map[string]string{"p": "f", "v": "v2"}}},
		{{Tool: "read", Args: map[string]string{"p": "f"}}},
	}}
	wf, err := NewWorkflowWithOptions(gen, WithTools(read, write))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := wf.Run(context.Background(), "p", "task"); err != nil {
		t.Fatal(err)
	}

	reads := observedResults(wf.Log.All(), "read")
	want := []string{"v1", "v2"}
	if len(reads) != len(want) {
		t.Fatalf("want %d read observations, got %d: %v", len(want), len(reads), reads)
	}
	for i := range want {
		if reads[i] != want[i] {
			t.Errorf("read %d: want %q, got %q (stale result replayed)", i, want[i], reads[i])
		}
	}
}

// TestPolicyRunsForEveryCall pins that admission is evaluated per call. A result
// cache consulted before the guards would serve a repeat call without asking the
// policy, so a permission revoked mid-run would not take effect.
func TestPolicyRunsForEveryCall(t *testing.T) {
	read := Tool{
		Name:        "read",
		Description: "read a key",
		Params:      map[string]Param{"p": {Type: "string", Required: true}},
		Handler:     func(a map[string]string) (string, error) { return "SECRET", nil },
	}

	identical := []ProposedEffect{{Tool: "read", Args: map[string]string{"p": "f"}}}
	gen := &scriptedGen{steps: [][]ProposedEffect{identical, identical, identical}}
	wf, err := NewWorkflowWithOptions(gen, WithTools(read))
	if err != nil {
		t.Fatal(err)
	}

	var consulted int32
	wf.Effects.AddPolicy(func(p *Process, e *Effect) (bool, string) {
		if atomic.AddInt32(&consulted, 1) > 1 {
			return false, "revoked"
		}
		return true, ""
	})

	if _, _, err := wf.Run(context.Background(), "p", "task"); err != nil {
		t.Fatal(err)
	}

	if got := atomic.LoadInt32(&consulted); got != 3 {
		t.Errorf("want policy consulted 3 times (once per proposed call), got %d", got)
	}
	if served := observedResults(wf.Log.All(), "read"); len(served) != 1 {
		t.Errorf("want 1 admitted call before revocation, got %d: %v", len(served), served)
	}
}
