package orchid

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// EventSink receives events as they are appended. Implementations must be
// thread-safe; Append may be called concurrently from multiple goroutines.
type EventSink interface {
	Append(e Event)
	Close() error
}

// writerSink adapts an io.Writer into an EventSink (JSONL format, one line per event).
type writerSink struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *writerSink) Append(e Event) {
	data, err := json.Marshal(newEventRecord(e))
	if err != nil {
		return
	}
	data = append(data, '\n')
	s.mu.Lock()
	s.w.Write(data)
	s.mu.Unlock()
}

func (s *writerSink) Close() error { return nil }

// EventType identifies what happened.
type EventType int

const (
	EventUserInput EventType = iota
	EventAssistantOutput
	EventEffectIntent
	EventEffectObserved
	EventEffectRejected
	EventProcessSpawned
	EventOutputTruncated
	EventRootOverride
	EventPromptSize
	EventPromptPressure
	EventHistoryTruncated
	// EventEffectMetadata records a rich handler's structured metadata. It is
	// audit-only: DeriveMessages ignores it, so it cannot duplicate the
	// EventEffectObserved that carries the tool result.
	EventEffectMetadata
)

// eventTypeNames is indexed by EventType. It is the serialised form of the
// type, so both String and parseEventType read it and cannot disagree.
var eventTypeNames = []string{
	"UserInput", "AssistantOutput",
	"EffectIntent", "EffectObserved", "EffectRejected",
	"ProcessSpawned", "OutputTruncated", "RootOverride",
	"PromptSize", "PromptPressure",
	"HistoryTruncated", "EffectMetadata",
}

func (t EventType) String() string {
	if int(t) < len(eventTypeNames) {
		return eventTypeNames[t]
	}
	return fmt.Sprintf("EventType(%d)", t)
}

// parseEventType resolves a serialised type name. An unrecognised name is an
// error rather than a zero value: silently reading an unknown event as
// EventUserInput would inject fabricated turns into a derived transcript.
func parseEventType(name string) (EventType, error) {
	for i, n := range eventTypeNames {
		if n == name {
			return EventType(i), nil
		}
	}
	return 0, fmt.Errorf("orchid: unknown event type %q", name)
}

// Event is an immutable record of something that happened.
type Event struct {
	ID        uint64
	Type      EventType
	Timestamp time.Time
	ProcessID string            // which process produced this event
	Data      map[string]string // payload — kept as strings for simplicity
}

// EventLog is an append-only, concurrency-safe log.
type EventLog struct {
	mu        sync.RWMutex
	events    []Event
	nextID    uint64
	writerIdx int // index into sinks of the SetSink writer; -1 if none
	sinks     []EventSink
}

func NewEventLog() *EventLog {
	return &EventLog{writerIdx: -1}
}

// SetSink sets an io.Writer that receives each event as a JSON line on append.
// It replaces any previously set writer sink but does not affect sinks added
// via AddSink. Passing nil removes the writer sink.
func (l *EventLog) SetSink(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if w == nil {
		if l.writerIdx >= 0 {
			// Remove the writer sink; copy-on-write.
			newSinks := make([]EventSink, 0, len(l.sinks)-1)
			for i, s := range l.sinks {
				if i != l.writerIdx {
					newSinks = append(newSinks, s)
				}
			}
			l.sinks = newSinks
			l.writerIdx = -1
		}
		return
	}
	ws := &writerSink{w: w}
	if l.writerIdx >= 0 {
		// Replace in place; copy-on-write the slice.
		newSinks := append([]EventSink(nil), l.sinks...)
		newSinks[l.writerIdx] = ws
		l.sinks = newSinks
	} else {
		// Append; copy-on-write.
		l.writerIdx = len(l.sinks)
		l.sinks = append(append([]EventSink(nil), l.sinks...), ws)
	}
}

// AddSink registers an additional EventSink that will receive all future events.
// Sinks are called in registration order. Use SetSink for the JSONL writer.
func (l *EventLog) AddSink(s EventSink) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sinks = append(append([]EventSink(nil), l.sinks...), s)
}

func (l *EventLog) Append(e Event) uint64 {
	l.mu.Lock()
	e.ID = l.nextID
	e.Timestamp = time.Now()
	l.nextID++
	l.events = append(l.events, e)
	sinks := l.sinks // slice header copy; safe after unlock because copy-on-write
	l.mu.Unlock()

	for _, s := range sinks {
		s.Append(e)
	}
	return e.ID
}

// Len returns the number of events.
func (l *EventLog) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.events)
}

// Slice returns events from start (inclusive) to end (exclusive).
// If end is 0, returns through the end of the log.
func (l *EventLog) Slice(start, end uint64) []Event {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if end == 0 || end > uint64(len(l.events)) {
		end = uint64(len(l.events))
	}
	if start >= end {
		return nil
	}
	out := make([]Event, end-start)
	copy(out, l.events[start:end])
	return out
}

// All returns a copy of all events.
func (l *EventLog) All() []Event {
	return l.Slice(0, 0)
}

// Since returns all events after the given ID (exclusive).
func (l *EventLog) Since(id uint64) []Event {
	return l.Slice(id+1, 0)
}

// EventLogVersion is the schema version stamped on every serialised event.
//
// The log is the source of truth for a run: the conversation, the audit trail,
// and any future replay all derive from it. An unversioned log cannot tell a
// reader that the writer's schema differed, so a shape change would be read as
// valid data and produce a subtly wrong transcript. Bump this when the meaning
// of a field changes, and teach readEventRecord to migrate the older shape.
const EventLogVersion = 1

type eventRecord struct {
	Version   int               `json:"v"`
	ID        uint64            `json:"id"`
	Type      string            `json:"type"`
	Timestamp time.Time         `json:"timestamp"`
	ProcessID string            `json:"process_id"`
	Data      map[string]string `json:"data"`
}

func newEventRecord(e Event) eventRecord {
	return eventRecord{
		Version:   EventLogVersion,
		ID:        e.ID,
		Type:      e.Type.String(),
		Timestamp: e.Timestamp,
		ProcessID: e.ProcessID,
		Data:      e.Data,
	}
}

func (r eventRecord) event() (Event, error) {
	// Version 0 is a log written before the schema was versioned. Its field
	// meanings match version 1, so it reads cleanly; anything above the version
	// this build knows was written by a newer writer and cannot be interpreted.
	if r.Version > EventLogVersion {
		return Event{}, fmt.Errorf(
			"orchid: event log version %d is newer than this build supports (%d)",
			r.Version, EventLogVersion,
		)
	}
	typ, err := parseEventType(r.Type)
	if err != nil {
		return Event{}, err
	}
	return Event{
		ID:        r.ID,
		Type:      typ,
		Timestamp: r.Timestamp,
		ProcessID: r.ProcessID,
		Data:      r.Data,
	}, nil
}

// ReadEvents decodes a JSONL event log, the format WriteTo and WithLogDir write.
// It is the counterpart that makes the log replayable: the events it returns can
// be passed to DeriveMessages to reconstruct the exact transcript a run sent.
//
// Decoding is strict. An unknown field, an unrecognised event type, or a version
// this build does not understand is an error, because a log that is silently
// misread is worse than one that refuses to load.
func ReadEvents(r io.Reader) ([]Event, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()

	var events []Event
	for line := 1; ; line++ {
		var rec eventRecord
		err := dec.Decode(&rec)
		if errors.Is(err, io.EOF) {
			return events, nil
		}
		if err != nil {
			return nil, fmt.Errorf("orchid: reading event %d: %w", line, err)
		}
		e, err := rec.event()
		if err != nil {
			return nil, fmt.Errorf("orchid: event %d: %w", line, err)
		}
		events = append(events, e)
	}
}

func (l *EventLog) WriteTo(w io.Writer) (int64, error) {
	l.mu.RLock()
	snapshot := make([]Event, len(l.events))
	copy(snapshot, l.events)
	l.mu.RUnlock()

	var total int64
	for _, e := range snapshot {
		data, err := json.Marshal(newEventRecord(e))
		if err != nil {
			return total, err
		}
		data = append(data, '\n')
		n, err := w.Write(data)
		total += int64(n)
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
