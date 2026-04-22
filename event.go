package orchid

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// EventType identifies what happened.
type EventType int

const (
	EventUserInput EventType = iota
	EventAssistantOutput
	EventEffectIntent
	EventEffectObserved
	EventEffectRejected
	EventMessage // inter-process message
	EventFork    // child process created
	EventJoin    // child result imported
	EventProcessSpawned
	EventProcessExited
)

func (t EventType) String() string {
	names := []string{
		"UserInput", "AssistantOutput",
		"EffectIntent", "EffectObserved", "EffectRejected",
		"Message", "Fork", "Join",
		"ProcessSpawned", "ProcessExited",
	}
	if int(t) < len(names) {
		return names[t]
	}
	return fmt.Sprintf("EventType(%d)", t)
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
	mu     sync.RWMutex
	events []Event
	nextID uint64
}

func NewEventLog() *EventLog {
	return &EventLog{}
}

// Append adds an event and returns its assigned ID.
func (l *EventLog) Append(e Event) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	e.ID = l.nextID
	e.Timestamp = time.Now()
	l.events = append(l.events, e)
	l.nextID++
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

type eventRecord struct {
	ID        uint64            `json:"id"`
	Type      string            `json:"type"`
	Timestamp time.Time         `json:"timestamp"`
	ProcessID string            `json:"process_id"`
	Data      map[string]string `json:"data"`
}

func (l *EventLog) WriteTo(w io.Writer) (int64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var total int64
	for _, e := range l.events {
		data, err := json.Marshal(eventRecord{
			ID:        e.ID,
			Type:      e.Type.String(),
			Timestamp: e.Timestamp,
			ProcessID: e.ProcessID,
			Data:      e.Data,
		})
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
