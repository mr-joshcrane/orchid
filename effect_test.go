package orchid

import (
	"errors"
	"testing"
)

func TestEffectTable_TrackAndGet(t *testing.T) {
	et := NewEffectTable()
	eff := &Effect{ID: "e1", Tool: "read", State: EffectPlanned}
	et.Track(eff)

	got := et.Get("e1")
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Tool != "read" {
		t.Fatalf("Tool = %q, want %q", got.Tool, "read")
	}
}

func TestEffectTable_GetMissing(t *testing.T) {
	et := NewEffectTable()
	if et.Get("nope") != nil {
		t.Fatal("should return nil for missing")
	}
}

func TestEffectTable_RegisterHandler(t *testing.T) {
	et := NewEffectTable()
	et.RegisterHandler("read", func(args map[string]string) (string, error) {
		return "content", nil
	})

	h, ok := et.GetHandler("read")
	if !ok {
		t.Fatal("handler should be registered")
	}
	result, err := h(nil)
	if err != nil || result != "content" {
		t.Fatalf("handler returned %q, %v", result, err)
	}
}

func TestEffectTable_GetHandler_Missing(t *testing.T) {
	et := NewEffectTable()
	_, ok := et.GetHandler("nope")
	if ok {
		t.Fatal("should not find unregistered handler")
	}
}

func TestEffectTable_CheckPolicies_AllPass(t *testing.T) {
	et := NewEffectTable()
	et.AddPolicy(func(p *Process, e *Effect) (bool, string) { return true, "" })
	et.AddPolicy(func(p *Process, e *Effect) (bool, string) { return true, "" })

	ok, _ := et.CheckPolicies(&Process{}, &Effect{})
	if !ok {
		t.Fatal("all policies pass, should be ok")
	}
}

func TestEffectTable_CheckPolicies_OneRejects(t *testing.T) {
	et := NewEffectTable()
	et.AddPolicy(func(p *Process, e *Effect) (bool, string) { return true, "" })
	et.AddPolicy(func(p *Process, e *Effect) (bool, string) { return false, "denied" })

	ok, reason := et.CheckPolicies(&Process{}, &Effect{})
	if ok {
		t.Fatal("should be rejected")
	}
	if reason != "denied" {
		t.Fatalf("reason = %q, want %q", reason, "denied")
	}
}

func TestEffectState_String(t *testing.T) {
	if EffectPlanned.String() != "Planned" {
		t.Fatalf("got %q", EffectPlanned.String())
	}
	if EffectState(99).String() != "EffectState(99)" {
		t.Fatalf("got %q", EffectState(99).String())
	}
}

func TestEffectHandler_Error(t *testing.T) {
	et := NewEffectTable()
	et.RegisterHandler("fail", func(args map[string]string) (string, error) {
		return "", errors.New("boom")
	})

	h, _ := et.GetHandler("fail")
	_, err := h(nil)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected boom error, got %v", err)
	}
}
