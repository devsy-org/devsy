package session

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type StateSuite struct{ suite.Suite }

func TestStateSuite(t *testing.T) { suite.Run(t, new(StateSuite)) }

var allStates = []State{
	StateQueued, StateSpawning, StateReady, StateBusy, StateIdle,
	StateReclaiming, StateDeleted, StateFailed, StateOrphaned,
}

// wantEdges is written out from the pool design, not read from transitions.
var wantEdges = map[State][]State{
	StateQueued:     {StateSpawning, StateReclaiming},
	StateSpawning:   {StateReady, StateFailed, StateReclaiming},
	StateReady:      {StateBusy, StateFailed, StateReclaiming},
	StateBusy:       {StateIdle, StateFailed, StateReclaiming},
	StateIdle:       {StateBusy, StateFailed, StateReclaiming},
	StateFailed:     {StateReclaiming},
	StateReclaiming: {StateDeleted, StateOrphaned},
	StateOrphaned:   {StateReclaiming, StateDeleted},
}

func (s *StateSuite) TestEveryPairMatchesTheDesign() {
	for _, from := range allStates {
		for _, to := range allStates {
			want := false
			for _, edge := range wantEdges[from] {
				want = want || edge == to
			}
			s.Equal(want, CanTransition(from, to), "%s to %s", from, to)
		}
	}
}

func (s *StateSuite) TestDeletedIsFinalAndEveryStateCanBeReclaimed() {
	for _, to := range allStates {
		s.False(CanTransition(StateDeleted, to))
	}
	for _, from := range allStates {
		if from == StateDeleted || from == StateReclaiming {
			continue
		}
		s.True(s.reaches(from, StateDeleted), "%s cannot reach deleted", from)
	}
}

func (s *StateSuite) TestUnknownStatesAreRefused() {
	s.False(State("").Known())
	s.False(State("running").Known())
	s.False(CanTransition("running", StateReady))
	s.False(CanTransition(StateQueued, "running"))
	for _, st := range allStates {
		s.True(st.Known())
	}
}

func (s *StateSuite) reaches(from, target State) bool {
	seen := map[State]bool{from: true}
	queue := []State{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == target {
			return true
		}
		for _, next := range allStates {
			if CanTransition(cur, next) && !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}
