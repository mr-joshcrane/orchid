package orchid

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestEventLog_WriteTo_ProducesValidJSONL(t *testing.T) {
	log := NewEventLog()
	log.Append(Event{
		Type:      EventUserInput,
		ProcessID: "proc_1",
		Data:      map[string]string{"content": "hello"},
	})
	log.Append(Event{
		Type:      EventAssistantOutput,
		ProcessID: "proc_1",
		Data:      map[string]string{"content": "world", "input_tokens": "100", "output_tokens": "25"},
	})

	var buf bytes.Buffer
	n, err := log.WriteTo(&buf)

	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("wrote 0 bytes")
	}

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	var record eventRecord
	if err := json.Unmarshal(lines[0], &record); err != nil {
		t.Fatalf("line 0 not valid JSON: %v", err)
	}
	if record.Type != "UserInput" {
		t.Fatalf("type = %q, want %q", record.Type, "UserInput")
	}
	if record.ProcessID != "proc_1" {
		t.Fatalf("process_id = %q", record.ProcessID)
	}
	if record.Data["content"] != "hello" {
		t.Fatalf("content = %q", record.Data["content"])
	}

	if err := json.Unmarshal(lines[1], &record); err != nil {
		t.Fatalf("line 1 not valid JSON: %v", err)
	}
	if record.Data["input_tokens"] != "100" {
		t.Fatalf("input_tokens = %q", record.Data["input_tokens"])
	}
}

// recordingSink records events for test assertions.
type recordingSink struct {
	mu     sync.Mutex
	events []Event
}

func (s *recordingSink) Append(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *recordingSink) Close() error { return nil }

func (s *recordingSink) all() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

func TestEventLog_AddSink_FansOutInOrder(t *testing.T) {
	log := NewEventLog()
	s1 := &recordingSink{}
	s2 := &recordingSink{}
	log.AddSink(s1)
	log.AddSink(s2)

	log.Append(Event{Type: EventUserInput, ProcessID: "p1"})

	e1 := s1.all()
	e2 := s2.all()
	if len(e1) != 1 || len(e2) != 1 {
		t.Fatalf("sink1=%d, sink2=%d, want 1 each", len(e1), len(e2))
	}
	if e1[0].ID != e2[0].ID {
		t.Fatalf("sinks received different IDs: %d vs %d", e1[0].ID, e2[0].ID)
	}
}

func TestEventLog_SetSink_ReplacesOnlyWriterSink(t *testing.T) {
	log := NewEventLog()
	custom := &recordingSink{}
	log.AddSink(custom)

	var buf1, buf2 bytes.Buffer
	log.SetSink(&buf1)
	log.SetSink(&buf2) // replaces buf1

	log.Append(Event{Type: EventUserInput, ProcessID: "p1", Data: map[string]string{"content": "hello"}})

	// custom sink should have received the event
	if len(custom.all()) != 1 {
		t.Fatalf("custom sink got %d events, want 1", len(custom.all()))
	}
	// buf1 should be empty (replaced)
	if buf1.Len() != 0 {
		t.Fatalf("buf1 should be empty after replacement, got %d bytes", buf1.Len())
	}
	// buf2 should have the event
	if buf2.Len() == 0 {
		t.Fatal("buf2 should have the event")
	}
}

func TestEventLog_SetSink_Nil_RemovesWriter(t *testing.T) {
	log := NewEventLog()
	custom := &recordingSink{}
	log.AddSink(custom)

	var buf bytes.Buffer
	log.SetSink(&buf)
	log.SetSink(nil) // remove writer

	log.Append(Event{Type: EventUserInput, ProcessID: "p1"})

	// writer should not have received anything
	if buf.Len() != 0 {
		t.Fatalf("buf should be empty after SetSink(nil), got %d bytes", buf.Len())
	}
	// custom sink should still work
	if len(custom.all()) != 1 {
		t.Fatalf("custom sink got %d events, want 1", len(custom.all()))
	}
}

func TestEventLog_Append_DoesNotHoldLockDuringSinkWrite(t *testing.T) {
	log := NewEventLog()

	// This sink calls log.Len() during Append. If the lock is held, this deadlocks.
	reentrant := &reentrantSink{log: log}
	log.AddSink(reentrant)

	done := make(chan struct{})
	go func() {
		log.Append(Event{Type: EventUserInput, ProcessID: "p1"})
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("Append deadlocked — lock held during sink write")
	}

	if reentrant.lenResult < 0 {
		t.Fatal("reentrant sink was not called")
	}
}

type reentrantSink struct {
	log       *EventLog
	lenResult int
}

func (s *reentrantSink) Append(e Event) {
	s.lenResult = s.log.Len()
}

func (s *reentrantSink) Close() error { return nil }

func TestWithSQLiteLog_Stub_ReturnsError(t *testing.T) {
	if sqliteAvailable() {
		t.Skip("sqlite build tag is active; stub test not applicable")
	}
	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{Text: "done"}
	})
	_, err := NewWorkflowWithOptions(gen, WithSQLiteLog(t.TempDir()+"/test.db"))
	if err == nil {
		t.Fatal("WithSQLiteLog without sqlite build tag should return error")
	}
}

func TestEventTypeNamesAreAligned(t *testing.T) {
	want := map[EventType]string{
		EventUserInput:        "UserInput",
		EventAssistantOutput:  "AssistantOutput",
		EventEffectIntent:     "EffectIntent",
		EventEffectObserved:   "EffectObserved",
		EventEffectRejected:   "EffectRejected",
		EventProcessSpawned:   "ProcessSpawned",
		EventOutputTruncated:  "OutputTruncated",
		EventRootOverride:     "RootOverride",
		EventPromptSize:       "PromptSize",
		EventPromptPressure:   "PromptPressure",
		EventHistoryTruncated: "HistoryTruncated",
		EventEffectMetadata:   "EffectMetadata",
	}

	for typ, name := range want {
		if got := typ.String(); got != name {
			t.Errorf("EventType(%d).String() = %q, want %q", int(typ), got, name)
		}
	}

	for i := 0; i <= int(EventEffectMetadata); i++ {
		if got := EventType(i).String(); strings.HasPrefix(got, "EventType(") {
			t.Errorf("EventType(%d) has no name; names slice is short", i)
		}
	}
	if got := EventType(int(EventEffectMetadata) + 1).String(); !strings.HasPrefix(got, "EventType(") {
		t.Errorf("EventType past the last constant resolved to %q; names slice has a stale entry", got)
	}
}
