package workspacelogs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFailedInvocationsKeepsExecutedResultOverLaterPlan(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "u")
	worker := newProducer(t, s, runID)
	parent := newProducer(t, s, runID)
	inv := NewID()
	workerSeq := newSeq(runID, worker)
	parentSeq := newSeq(runID, parent)

	mustAppend(t, s, workerSeq.invocation(inv, hookCreate, InvocationFailed))
	mustAppend(t, s, parentSeq.invocation(inv, hookCreate, InvocationDeferred))

	failed, err := s.FailedInvocations(runID)
	require.NoError(t, err)
	assert.Equal(t, []string{inv}, failed)
}

func TestLatestStatesReportsUnfinishedStepsAsUnknownOnceRunIsOver(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "u")
	pid := newProducer(t, s, runID)
	seq := newSeq(runID, pid)
	started, planned, skipped := NewID(), NewID(), NewID()
	mustAppend(
		t, s,
		seq.invocation(started, hookCreate, InvocationRunning),
		seq.invocation(planned, hookCreate, InvocationPending),
		seq.invocation(skipped, hookCreate, InvocationSkipped),
	)

	m, err := s.Manifest(runID)
	require.NoError(t, err)
	states, _, err := s.latestStates(m)
	require.NoError(t, err)
	assert.Equal(t, InvocationRunning, states[started], "a live run may still finish the step")

	mustAppend(t, s, seq.terminal(true))
	m, err = s.Manifest(runID)
	require.NoError(t, err)
	states, _, err = s.latestStates(m)
	require.NoError(t, err)
	assert.Equal(t, InvocationUnknown, states[started])
	assert.Equal(t, InvocationUnknown, states[planned])
	assert.Equal(t, InvocationSkipped, states[skipped])
}
