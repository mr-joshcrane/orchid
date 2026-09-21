package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func samplePlan() *Plan {
	return &Plan{
		Goal:       "build the thing",
		Invariants: []string{"tests pass", "no new deps"},
		Reflection: "went fine",
		Steps: []Step{
			{
				ID:         "step-1",
				Intent:     "add the type",
				Signatures: "type Foo struct{}",
				Files:      []string{"foo.go"},
				DependsOn:  nil,
				Verify:     Verify{Cmd: "go build ./...", Expect: ""},
				Status:     StatusPending,
				Attempts:   []Attempt{{Summary: "tried", Gate: "fail", Feedback: "retry"}},
				Notes:      "careful here",
			},
			{
				ID:        "step-2",
				Intent:    "wire it up",
				Files:     []string{"main.go"},
				DependsOn: []string{"step-1"},
				Verify:    Verify{Cmd: "go test ./...", Expect: "ok"},
				Status:    StatusDone,
			},
		},
	}
}

func TestPlanRoundTrip(t *testing.T) {
	original := samplePlan()

	path := filepath.Join(t.TempDir(), "roundtrip.json")
	if err := original.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := LoadPlan(path)
	if err != nil {
		t.Fatalf("LoadPlan: %v", err)
	}

	if !reflect.DeepEqual(original, reloaded) {
		t.Fatalf("round-trip mismatch:\n original = %+v\n reloaded = %+v", original, reloaded)
	}
}

func TestPlanModelFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fields.json")
	if err := samplePlan().Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	p, err := LoadPlan(path)
	if err != nil {
		t.Fatalf("LoadPlan: %v", err)
	}

	if p.Goal == "" {
		t.Error("goal did not load")
	}
	if len(p.Invariants) == 0 {
		t.Error("expected invariants to load")
	}
	if len(p.Steps) == 0 {
		t.Fatal("expected steps to load")
	}

	first := p.Steps[0]
	if first.ID == "" || first.Intent == "" {
		t.Error("step id/intent did not load")
	}
	if first.Signatures == "" {
		t.Error("step signatures did not load")
	}
	if first.Verify.Cmd == "" {
		t.Error("step verify.cmd did not load")
	}
	if first.Status != StatusPending {
		t.Errorf("first step status = %q, want %q", first.Status, StatusPending)
	}
	if len(first.Attempts) == 0 || first.Attempts[0].Gate == "" {
		t.Error("step attempts did not load")
	}
	if len(p.Steps[1].DependsOn) == 0 {
		t.Error("step dependsOn did not load")
	}
}
