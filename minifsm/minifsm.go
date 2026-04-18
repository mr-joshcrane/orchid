package minifsm

import (
	"fmt"
)

// transition holds the destination state and an action to execute upon transition.
type transition struct {
	to     string
	action func() error
}

// FSM represents the Finite State Machine.
type FSM struct {
	initial     string
	current     string
	transitions map[string]map[string]transition
}

// New creates and returns a new FSM initialized with the given initial state.
func New(initial string) *FSM {
	return &FSM{
		initial:     initial,
		current:     initial,
		transitions: make(map[string]map[string]transition),
	}
}

// On defines a transition for the FSM.
// It sets up a transition from 'from' state, triggered by 'event', to 'to' state,
// executing the provided 'action' function upon successful transition.
func (f *FSM) On(from string, event string, to string, action func() error) {
	if f.transitions == nil {
		f.transitions = make(map[string]map[string]transition)
	}
	if _, ok := f.transitions[from]; !ok {
		f.transitions[from] = make(map[string]transition)
	}
	f.transitions[from][event] = transition{
		to:     to,
		action: action,
	}
}

// Send attempts to transition the FSM based on the given event.
// It executes the associated action and updates the current state if a valid transition exists.
// Returns an error if the FSM is uninitialized, the event is invalid for the current state,
// or if the transition action fails.
func (f *FSM) Send(event string) error {
	if f.current == "" {
		return fmt.Errorf("fsm is not initialized or has no current state")
	}

	if !f.Can(event) {
		return fmt.Errorf("cannot send event %s from state %s", event, f.current)
	}

	if transitions, ok := f.transitions[f.current]; ok {
		if t, ok := transitions[event]; ok {
			if err := t.action(); err != nil {
				return fmt.Errorf("action failed for event %s: %w", event, err)
			}
			f.current = t.to
			return nil
		}
	}

	return fmt.Errorf("no transition found for event %s from state %s", event, f.current)
}

// State returns the current state of the FSM.
func (f *FSM) State() string {
	return f.current
}

// Can checks if a transition for the given event is possible from the current state.
func (f *FSM) Can(event string) bool {
	transitionsFromCurrent, ok := f.transitions[f.current]
	if !ok {
		return false
	}
	_, ok = transitionsFromCurrent[event]
	return ok
}

// Reset sets the FSM back to its initial state.
func (f *FSM) Reset() {
	f.current = f.initial
}