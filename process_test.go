package orchid

import "testing"

func TestProcessTable_AddGet(t *testing.T) {
	pt := NewProcessTable()
	p := &Process{ID: "p1", Name: "test"}
	pt.Add(p)

	got := pt.Get("p1")
	if got == nil {
		t.Fatal("Get returned nil for added process")
	}
	if got.Name != "test" {
		t.Fatalf("Name = %q, want %q", got.Name, "test")
	}
}

func TestProcessTable_GetMissing(t *testing.T) {
	pt := NewProcessTable()
	if pt.Get("nope") != nil {
		t.Fatal("Get should return nil for missing process")
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

func TestProcess_HasPermission(t *testing.T) {
	p := &Process{
		Permissions: map[Permission]bool{
			PermissionFromTool("read"):  true,
			PermissionFromTool("write"): true,
		},
	}

	if !p.HasPermission(PermissionFromTool("read")) {
		t.Fatal("should have read")
	}
	if p.HasPermission(PermissionFromTool("bash")) {
		t.Fatal("should not have bash")
	}
}
