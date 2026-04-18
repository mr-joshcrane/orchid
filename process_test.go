package orchid

import "testing"

func TestProcessTable_AddGet(t *testing.T) {
	pt := NewProcessTable()
	p := &Process{ID: "p1", Spec: ProcessSpec{Name: "test"}}
	pt.Add(p)

	got := pt.Get("p1")
	if got == nil {
		t.Fatal("Get returned nil for added process")
	}
	if got.Spec.Name != "test" {
		t.Fatalf("Name = %q, want %q", got.Spec.Name, "test")
	}
}

func TestProcessTable_GetMissing(t *testing.T) {
	pt := NewProcessTable()
	if pt.Get("nope") != nil {
		t.Fatal("Get should return nil for missing process")
	}
}

func TestProcessTable_Remove(t *testing.T) {
	pt := NewProcessTable()
	pt.Add(&Process{ID: "p1"})
	pt.Remove("p1")

	if pt.Get("p1") != nil {
		t.Fatal("process should be removed")
	}
}

func TestProcessTable_All(t *testing.T) {
	pt := NewProcessTable()
	pt.Add(&Process{ID: "p1"})
	pt.Add(&Process{ID: "p2"})

	all := pt.All()
	if len(all) != 2 {
		t.Fatalf("All() len = %d, want 2", len(all))
	}
}

func TestProcess_HasCapability(t *testing.T) {
	p := &Process{
		Capabilities: map[Capability]bool{CapRead: true, CapWrite: true},
	}

	if !p.HasCapability(CapRead) {
		t.Fatal("should have read")
	}
	if p.HasCapability(CapBash) {
		t.Fatal("should not have bash")
	}
}
