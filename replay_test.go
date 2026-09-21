package orchid

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestLogRoundTripsToIdenticalTranscript(t *testing.T) {
	slow := Tool{
		Name:           "work",
		Description:    "does work",
		Params:         map[string]Param{"i": {Type: "string", Required: true}},
		Parallelizable: true,
		Handler:        func(a map[string]string) (string, error) { return "result-" + a["i"], nil },
	}

	gen := &scriptedGen{steps: [][]ProposedEffect{
		{
			{Tool: "work", Args: map[string]string{"i": "a"}, ToolUseID: "c1"},
			{Tool: "work", Args: map[string]string{"i": "b"}, ToolUseID: "c2"},
		},
		{{Tool: "work", Args: map[string]string{"i": "c"}, ToolUseID: "c3"}},
	}}

	wf, err := NewWorkflowWithOptions(gen, WithTools(slow), WithEffectConcurrency(4))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := wf.Run(context.Background(), "purpose", "task"); err != nil {
		t.Fatal(err)
	}

	original := DeriveMessages(wf.Log.All())
	if len(original) == 0 {
		t.Fatal("run produced no transcript")
	}
	assertToolCallsAnswered(t, original)

	var buf bytes.Buffer
	if _, err := wf.Log.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	replayed, err := ReadEvents(&buf)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(replayed) != wf.Log.Len() {
		t.Fatalf("read %d events, wrote %d", len(replayed), wf.Log.Len())
	}

	fromDisk := DeriveMessages(replayed)
	if !reflect.DeepEqual(original, fromDisk) {
		t.Errorf("transcript changed across a log round-trip:\n in-memory = %+v\n from disk = %+v", original, fromDisk)
	}
	assertToolCallsAnswered(t, fromDisk)
}

func TestReadEventsRejectsNewerVersion(t *testing.T) {
	line := `{"v":9999,"id":0,"type":"UserInput","timestamp":"2026-01-01T00:00:00Z","process_id":"p","data":{"content":"hi"}}`

	_, err := ReadEvents(strings.NewReader(line))
	if err == nil {
		t.Fatal("expected an error for a future schema version")
	}
	if !strings.Contains(err.Error(), "newer than this build") {
		t.Errorf("error should explain the version mismatch, got %q", err)
	}
}

func TestReadEventsAcceptsUnversionedLog(t *testing.T) {
	line := `{"id":0,"type":"UserInput","timestamp":"2026-01-01T00:00:00Z","process_id":"p","data":{"content":"hi"}}`

	events, err := ReadEvents(strings.NewReader(line))
	if err != nil {
		t.Fatalf("an unversioned log should read as version 1: %v", err)
	}
	if len(events) != 1 || events[0].Type != EventUserInput {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestReadEventsRejectsUnknownType(t *testing.T) {
	line := `{"v":1,"id":0,"type":"SomethingElse","timestamp":"2026-01-01T00:00:00Z","process_id":"p","data":{}}`

	_, err := ReadEvents(strings.NewReader(line))
	if err == nil {
		t.Fatal("expected an error for an unknown event type")
	}
	if !strings.Contains(err.Error(), "SomethingElse") {
		t.Errorf("error should name the unknown type, got %q", err)
	}
}

func TestReadEventsRejectsUnknownField(t *testing.T) {
	line := `{"v":1,"id":0,"type":"UserInput","timestamp":"2026-01-01T00:00:00Z","process_id":"p","data":{},"surprise":true}`

	if _, err := ReadEvents(strings.NewReader(line)); err == nil {
		t.Fatal("expected an error for an unknown field")
	}
}

func TestWrittenEventsCarryTheVersion(t *testing.T) {
	log := NewEventLog()
	var sink bytes.Buffer
	log.SetSink(&sink)
	log.Append(Event{Type: EventUserInput, Data: map[string]string{"content": "hi"}})

	var written bytes.Buffer
	if _, err := log.WriteTo(&written); err != nil {
		t.Fatal(err)
	}

	for name, out := range map[string]string{"SetSink": sink.String(), "WriteTo": written.String()} {
		if !strings.Contains(out, `"v":1`) {
			t.Errorf("%s output does not carry the schema version: %s", name, out)
		}
	}
}
