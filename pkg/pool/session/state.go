// Package session holds the pool's session records: the state machine, the
// atomic on-disk store and the single-owner pool lock.
package session

import "slices"

// State is where a session is in its life.
type State string

// States, from the pool design.
const (
	StateQueued     State = "queued"
	StateSpawning   State = "spawning"
	StateReady      State = "ready"
	StateBusy       State = "busy"
	StateIdle       State = "idle"
	StateReclaiming State = "reclaiming"
	StateDeleted    State = "deleted"
	StateFailed     State = "failed"
	StateOrphaned   State = "orphaned"
)

// transitions lists every allowed edge. Any live state may go to reclaiming
// (ttl or an exit path), and failed always does. A session reclaimed five
// times without success becomes orphaned and is still retried.
var transitions = map[State][]State{
	StateQueued:     {StateSpawning, StateReclaiming},
	StateSpawning:   {StateReady, StateFailed, StateReclaiming},
	StateReady:      {StateBusy, StateFailed, StateReclaiming},
	StateBusy:       {StateIdle, StateFailed, StateReclaiming},
	StateIdle:       {StateBusy, StateFailed, StateReclaiming},
	StateFailed:     {StateReclaiming},
	StateReclaiming: {StateDeleted, StateOrphaned},
	StateOrphaned:   {StateReclaiming, StateDeleted},
	StateDeleted:    nil,
}

// Known reports whether s is one of the defined states.
func (s State) Known() bool {
	_, ok := transitions[s]
	return ok
}

func (s State) canCountReclaimAttempt() bool {
	return s == StateReclaiming || s == StateOrphaned
}

// CanTransition reports whether the state machine allows from to go to to.
func CanTransition(from, to State) bool {
	return slices.Contains(transitions[from], to)
}
