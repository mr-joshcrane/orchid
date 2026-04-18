package orchid

import (
	"sync"
	"testing"
)

func TestEventLog_Append_AssignsSequentialIDs(t *testing.T) {
	log := NewEventLog()

	id0 := log.Append(Event{Type: EventUserInput, ProcessID: "p1"})
	id1 := log.Append(Event{Type: EventAssistantOutput, ProcessID: "p1"})

	if id0 != 0 {
		t.Fatalf("first event ID = %d, want 0", id0)
	}
	if id1 != 1 {
		t.Fatalf("second event ID = %d, want 1", id1)
	}
}

func TestEventLog_Append_SetsTimestamp(t *testing.T) {
	log := NewEventLog()
	log.Append(Event{Type: EventUserInput, ProcessID: "p1"})

	events := log.All()
	if events[0].Timestamp.IsZero() {
		t.Fatal("timestamp should be set by Append")
	}
}

func TestEventLog_Len(t *testing.T) {
	log := NewEventLog()
	if log.Len() != 0 {
		t.Fatal("empty log should have len 0")
	}
	log.Append(Event{Type: EventUserInput})
	log.Append(Event{Type: EventUserInput})
	if log.Len() != 2 {
		t.Fatalf("len = %d, want 2", log.Len())
	}
}

func TestEventLog_Slice(t *testing.T) {
	log := NewEventLog()
	for i := 0; i < 5; i++ {
		log.Append(Event{Type: EventUserInput, Data: map[string]string{"i": string(rune('0' + i))}})
	}

	// given: slice [1,3)
	s := log.Slice(1, 3)
	if len(s) != 2 {
		t.Fatalf("slice len = %d, want 2", len(s))
	}
	if s[0].ID != 1 || s[1].ID != 2 {
		t.Fatalf("slice IDs = [%d,%d], want [1,2]", s[0].ID, s[1].ID)
	}

	// when: end=0 means through end
	s2 := log.Slice(3, 0)
	if len(s2) != 2 {
		t.Fatalf("slice-to-end len = %d, want 2", len(s2))
	}

	// then: start >= end returns nil
	s3 := log.Slice(5, 3)
	if s3 != nil {
		t.Fatal("start >= end should return nil")
	}
}

func TestEventLog_All_ReturnsCopy(t *testing.T) {
	log := NewEventLog()
	log.Append(Event{Type: EventUserInput})

	all := log.All()
	all[0].ProcessID = "mutated"

	// original should be unchanged
	original := log.All()
	if original[0].ProcessID == "mutated" {
		t.Fatal("All() should return a copy, not a reference")
	}
}

func TestEventLog_Since(t *testing.T) {
	log := NewEventLog()
	log.Append(Event{Type: EventUserInput}) // 0
	log.Append(Event{Type: EventUserInput}) // 1
	log.Append(Event{Type: EventUserInput}) // 2

	s := log.Since(0)
	if len(s) != 2 {
		t.Fatalf("Since(0) len = %d, want 2", len(s))
	}
	if s[0].ID != 1 {
		t.Fatalf("Since(0) first ID = %d, want 1", s[0].ID)
	}
}

func TestEventLog_ConcurrentAppend(t *testing.T) {
	log := NewEventLog()
	n := 100
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			log.Append(Event{Type: EventUserInput})
		}()
	}
	wg.Wait()

	if log.Len() != n {
		t.Fatalf("concurrent append: len = %d, want %d", log.Len(), n)
	}

	// all IDs should be unique
	seen := make(map[uint64]bool)
	for _, e := range log.All() {
		if seen[e.ID] {
			t.Fatalf("duplicate ID: %d", e.ID)
		}
		seen[e.ID] = true
	}
}

func TestEventType_String(t *testing.T) {
	if EventUserInput.String() != "UserInput" {
		t.Fatalf("EventUserInput.String() = %q", EventUserInput.String())
	}
	if EventType(99).String() != "EventType(99)" {
		t.Fatalf("unknown type string = %q", EventType(99).String())
	}
}
