package workspacelogs

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionRecordsAndFinishesRun(t *testing.T) {
	s, _ := newTestStore(t)
	sess := StartSessionIn(s, SessionOptions{
		Run:      RunSpec{Context: testContext, Command: "up", WorkspaceUID: "u"},
		Location: LocationHost,
	})
	require.NotNil(t, sess)

	inv := NewID()
	w := sess.Recorder.Writer(inv, StreamStdout)
	_, _ = w.Write([]byte("hi"))
	require.NoError(t, w.Close())
	ctx := sess.Context(context.Background())
	assert.Same(t, sess.Recorder, RecorderFrom(ctx))
	sess.Finish(nil, true)

	m, err := s.Manifest(sess.RunID)
	require.NoError(t, err)
	assert.Equal(t, CommandSucceeded, m.CommandState)
	assert.Equal(t, CaptureComplete, m.CaptureState)
}

func TestSessionJoinsRunAndRecordsFailure(t *testing.T) {
	s, _ := newTestStore(t)
	first := StartSessionIn(s, SessionOptions{Run: RunSpec{Context: testContext, Command: "up"}})
	require.NotNil(t, first)

	joined := StartSessionIn(s, SessionOptions{
		RunID: first.RunID, ParentProducerID: first.ProducerID(),
		Run: RunSpec{Context: testContext, Command: "up"}, Location: LocationContainer,
	})
	require.NotNil(t, joined)
	assert.Equal(t, first.RunID, joined.RunID)
	joined.BindWorkspace("uid-9", "ws")
	joined.Finish(errors.New("boom"), false)
	first.Finish(nil, true)

	m, err := s.Manifest(first.RunID)
	require.NoError(t, err)
	assert.Equal(
		t, CommandSucceeded, m.CommandState, "only the sealing process writes the command state",
	)
	require.NotNil(t, m.WorkspaceUID)
	assert.Equal(t, "uid-9", *m.WorkspaceUID)
	assert.Len(t, m.Producers, 2)
	assert.Equal(t, first.ProducerID(), m.Producers[1].ParentProducerID)
}

func TestNilSessionIsInert(t *testing.T) {
	var sess *Session
	ctx := context.Background()

	assert.Equal(t, ctx, sess.Context(ctx))
	assert.Empty(t, sess.ProducerID())
	sess.BindWorkspace("u", "w")
	sess.Finish(errors.New("x"), true)
}

func TestFinishMapsCancellation(t *testing.T) {
	s, _ := newTestStore(t)
	sess := StartSessionIn(s, SessionOptions{Run: RunSpec{Context: testContext, Command: "up"}})
	require.NotNil(t, sess)

	sess.Finish(context.Canceled, true)

	m, err := s.Manifest(sess.RunID)
	require.NoError(t, err)
	assert.Equal(t, CommandCanceled, m.CommandState)
}

func TestChildFinishDoesNotWriteCommandState(t *testing.T) {
	s, _ := newTestStore(t)
	first := StartSessionIn(s, SessionOptions{Run: RunSpec{Context: testContext, Command: "up"}})
	require.NotNil(t, first)
	joined := StartSessionIn(s, SessionOptions{
		RunID: first.RunID, ParentProducerID: first.ProducerID(),
		Run: RunSpec{Context: testContext, Command: "up"}, Location: LocationContainer,
	})
	require.NotNil(t, joined)

	joined.Finish(errors.New("boom"), false)

	m, err := s.Manifest(first.RunID)
	require.NoError(t, err)
	assert.Equal(t, CommandRunning, m.CommandState)
}

func TestStartSessionFailsAndSealsRunWhenRecorderCannotStart(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "u")
	for range MaxProducers {
		newProducer(t, s, runID)
	}

	sess := StartSessionIn(s, SessionOptions{
		RunID: runID, Run: RunSpec{Context: testContext, Command: "up", WorkspaceUID: "u"},
	})

	assert.Nil(t, sess)
	m, err := s.Manifest(runID)
	require.NoError(t, err)
	assert.Equal(t, CommandFailed, m.CommandState)
	assert.True(t, m.DiscoverySealed)
}
