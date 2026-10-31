package workspacelogs

import (
	"errors"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRecorder(t *testing.T, s *Store, runID string, redactor *secrets.Redactor) *Recorder {
	t.Helper()
	rec, err := NewRecorder(RecorderOptions{
		Backend: s, RunID: runID, Location: LocationContainer, Redactor: redactor,
	})
	require.NoError(t, err)

	return rec
}

func TestRecorderRoundTripsInvocationOutputAndExit(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "uid")
	rec := newTestRecorder(t, s, runID, nil)
	inv := NewID()
	exit := 7
	idx := 0

	rec.Invocation(
		Invocation{
			ID:    inv,
			Phase: "setup",
			Hook:  hookCreate,
			Index: &idx,
			State: InvocationRunning,
		},
	)
	out, errW := rec.Writer(inv, StreamStdout), rec.Writer(inv, StreamStderr)
	_, _ = out.Write([]byte("hello "))
	_, _ = errW.Write([]byte("oops\n"))
	_, _ = out.Write([]byte("world"))
	require.NoError(t, out.Close())
	require.NoError(t, errW.Close())
	rec.Invocation(
		Invocation{ID: inv, Hook: hookCreate, State: InvocationFailed, ExitCode: &exit},
	)
	rec.Close(ProducerTerminal, true)
	require.NoError(t, rec.Err())

	recs := readAll(t, s, ReadRequest{RunID: runID, Hook: hookCreate})
	var stdout, stderr string
	var final *Record
	for i := range recs {
		switch {
		case recs[i].Kind == KindOutput && recs[i].Stream == StreamStdout:
			stdout += recs[i].Text
		case recs[i].Kind == KindOutput && recs[i].Stream == StreamStderr:
			stderr += recs[i].Text
		case recs[i].Kind == KindInvocation:
			final = &recs[i]
		}
	}
	assert.Equal(t, "hello world", stdout, "a final fragment without a newline is kept")
	assert.Equal(t, "oops\n", stderr)
	require.NotNil(t, final)
	assert.Equal(t, InvocationFailed, final.State)
	require.NotNil(t, final.ExitCode)
	assert.Equal(t, 7, *final.ExitCode)

	resp, err := s.Read(ReadRequest{RunID: runID})
	require.NoError(t, err)
	assert.True(t, resp.Complete)
}

func TestRecorderRedactsSecretsSplitAcrossWrites(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "uid")
	redactor := secrets.NewRedactor([]string{"API_TOKEN=hunter2-secret-value"})
	rec := newTestRecorder(t, s, runID, redactor)
	inv := NewID()

	w := rec.Writer(inv, StreamStdout)
	for _, part := range []string{"token=hunter2-", "secret", "-value done\n"} {
		_, err := w.Write([]byte(part))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())

	got := texts(readAll(t, s, ReadRequest{RunID: runID}))
	assert.NotContains(t, got, "hunter2")
	assert.Contains(t, got, "token=")
	assert.Contains(t, got, "done")

	raw := rawRunFiles(t, s, runID)
	assert.NotContains(t, raw, "hunter2", "the secret is absent from files on disk")
}

func TestRecorderKeepsStreamsIndependent(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "uid")
	redactor := secrets.NewRedactor([]string{"API_TOKEN=abcdef-secret"})
	rec := newTestRecorder(t, s, runID, redactor)
	inv := NewID()
	out, errW := rec.Writer(inv, StreamStdout), rec.Writer(inv, StreamStderr)

	// Halves of the secret arrive on different streams: neither stream holds
	// the whole value, so neither is masked, and neither half is swallowed.
	_, _ = out.Write([]byte("abcdef-"))
	_, _ = errW.Write([]byte("secret"))
	require.NoError(t, out.Close())
	require.NoError(t, errW.Close())

	got := texts(readAll(t, s, ReadRequest{RunID: runID}))
	assert.Contains(t, got, "abcdef-")
	assert.Contains(t, got, "secret")
}

func TestRecorderSplitsMultibyteRunesAcrossWrites(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "uid")
	rec := newTestRecorder(t, s, runID, nil)
	inv := NewID()
	w := rec.Writer(inv, StreamStdout)
	rune3 := []byte("€")

	_, _ = w.Write(append([]byte("a"), rune3[:2]...))
	_, _ = w.Write(append(append([]byte{}, rune3[2:]...), 'b'))
	require.NoError(t, w.Close())

	assert.Equal(t, "a€b", texts(readAll(t, s, ReadRequest{RunID: runID})))
}

func TestRecorderChunksLargeWrites(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "uid")
	rec := newTestRecorder(t, s, runID, nil)
	inv := NewID()
	big := strings.Repeat("q", MaxChunkBytes*3+10)

	w := rec.Writer(inv, StreamStdout)
	_, _ = w.Write([]byte(big))
	require.NoError(t, w.Close())

	recs := readAll(t, s, ReadRequest{RunID: runID})
	var outputs int
	for i := range recs {
		if recs[i].Kind == KindOutput {
			outputs++
			assert.LessOrEqual(t, len(recs[i].Text), MaxChunkBytes)
		}
	}
	assert.Equal(t, 4, outputs)
	assert.Equal(t, big, texts(recs))
}

type failingBackend struct {
	Backend
	appendErr  error
	appends    int
	conditions []Condition
}

func (f *failingBackend) Append(recs []Record) (AppendResult, error) {
	f.appends++
	if f.appends > 1 {
		return AppendResult{}, f.appendErr
	}

	return f.Backend.Append(recs)
}

func (f *failingBackend) AddCondition(runID string, c Condition) error {
	f.conditions = append(f.conditions, c)

	return f.Backend.AddCondition(runID, c)
}

func TestWriterNeverFailsWhenStorageDoes(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "uid")
	backend := &failingBackend{Backend: s, appendErr: errors.New("disk full")}
	rec, err := NewRecorder(RecorderOptions{Backend: backend, RunID: runID, Location: LocationHost})
	require.NoError(t, err)
	w := rec.Writer(NewID(), StreamStdout)

	for range 5 {
		n, writeErr := w.Write([]byte("still running\n"))
		require.NoError(t, writeErr, "the producing process never sees a log error")
		assert.Equal(t, len("still running\n"), n)
	}
	require.NoError(t, w.Close())

	require.EqualError(t, rec.Err(), "disk full", "the first error is kept")
	assert.Equal(t, 2, backend.appends, "the failed sink is disabled after its first error")
	assert.Equal(t, []Condition{ConditionWriteError}, backend.conditions)
	m, err := s.Manifest(runID)
	require.NoError(t, err)
	assert.Equal(t, CapturePartial, m.CaptureState)
}

func TestNewRecorderReportsRegistrationFailureAndStaysUsable(t *testing.T) {
	s, _ := newTestStore(t)
	rec, err := NewRecorder(RecorderOptions{Backend: s, RunID: NewID()})

	require.Error(t, err)
	require.NotNil(t, rec)
	w := rec.Writer(NewID(), StreamStdout)
	n, writeErr := w.Write([]byte("ignored"))
	require.NoError(t, writeErr)
	assert.Equal(t, len("ignored"), n)
	rec.Invocation(Invocation{ID: NewID(), State: InvocationRunning})
	rec.Close(ProducerTerminal, true)
}

func TestRecorderTruncatesOptionalDisplayFields(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "uid")
	rec := newTestRecorder(t, s, runID, nil)

	rec.Invocation(Invocation{
		ID: NewID(), Hook: hookCreate, State: InvocationSkipped,
		Reason: strings.Repeat("r", MaxDetailBytes*3),
	})

	require.NoError(t, rec.Err())
	for _, r := range readAll(t, s, ReadRequest{RunID: runID}) {
		assert.LessOrEqual(t, len(r.Reason), MaxDetailBytes)
	}
}

func rawRunFiles(t *testing.T, s *Store, runID string) string {
	t.Helper()
	dir, err := s.runDir(runID)
	require.NoError(t, err)

	return readTree(t, dir)
}

func TestReservedChildIsKnownBeforeItStarts(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "uid")
	parent := newTestRecorder(t, s, runID, nil)

	childID := parent.ReserveChild(LocationContainer)
	parent.SealDiscovery()
	parent.Close(ProducerTerminal, false)

	m, err := s.Manifest(runID)
	require.NoError(t, err)
	require.Len(t, m.Producers, 2)
	assert.Equal(t, childID, m.Producers[1].ProducerID)
	assert.Equal(t, ProducerReserved, m.Producers[1].State)
	assert.True(t, m.DiscoverySealed)
	assert.False(t, m.Complete(), "a reserved child that has not run keeps the run incomplete")

	child, err := NewRecorder(RecorderOptions{
		Backend: s, RunID: runID, ProducerID: childID, ParentProducerID: parent.ProducerID(),
	})
	require.NoError(t, err)
	assert.Equal(t, childID, child.ProducerID())
	child.Close(ProducerTerminal, false)

	m, err = s.Manifest(runID)
	require.NoError(t, err)
	assert.Len(t, m.Producers, 2, "the child reuses its reserved entry")
	assert.True(t, m.Complete())
}

func TestUnobservedChildLaunchIsUnavailableNotEmptySuccess(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "uid")
	parent := newTestRecorder(t, s, runID, nil)
	childID := parent.ReserveChild(LocationContainer)

	parent.MarkChild(childID, ProducerUnavailable, "launch_not_observed")
	parent.SealDiscovery()
	parent.Close(ProducerTerminal, false)

	resp, err := s.Read(ReadRequest{RunID: runID})
	require.NoError(t, err)
	assert.False(t, resp.Complete)
	assert.Equal(t, CapturePartial, resp.Run.CaptureState)
	var unavailable bool
	for _, a := range resp.Availability {
		unavailable = unavailable || (a.ProducerID == childID && !a.Available)
	}
	assert.True(t, unavailable)
}

func TestMirroredProducerIntroducesItselfWithItsFirstRecord(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "uid")
	pid := NewID()
	seq := newSeq(runID, pid)
	first := seq.rec(
		Record{Kind: KindProducer, ProducerState: ProducerRunning, Location: LocationContainer},
	)

	_, err := s.Append([]Record{seq.output(NewID(), StreamStdout, "no intro")})
	require.ErrorIs(t, err, ErrNotFound, "output cannot introduce a producer")

	seq.next = 1
	res, err := s.Append([]Record{first})
	require.NoError(t, err)
	assert.Equal(t, uint64(1), res.Accepted)
	m, err := s.Manifest(runID)
	require.NoError(t, err)
	assert.Equal(t, LocationContainer, m.Producers[0].Location)
}

func TestResumedWorkerDoesNotRepeatThePlan(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "uid")
	rec, err := NewRecorder(RecorderOptions{Backend: s, RunID: runID, Resumed: true})
	require.NoError(t, err)
	inv := NewID()

	rec.Invocation(Invocation{ID: inv, Hook: hookCreate, State: InvocationPending})
	rec.Invocation(Invocation{ID: inv, Hook: hookCreate, State: InvocationDeferred})
	rec.Invocation(
		Invocation{
			ID:     NewID(),
			Hook:   hookCreate,
			State:  InvocationSkipped,
			Reason: "no_command_configured",
		},
	)
	rec.Invocation(
		Invocation{
			ID:     NewID(),
			Hook:   hookCreate,
			State:  InvocationSkipped,
			Reason: "already_attempted",
		},
	)
	rec.Invocation(Invocation{ID: inv, Hook: hookCreate, State: InvocationRunning})

	var states []InvocationState
	for _, r := range readAll(t, s, ReadRequest{RunID: runID}) {
		if r.Kind == KindInvocation {
			states = append(states, r.State)
		}
	}
	assert.Equal(t, []InvocationState{InvocationSkipped, InvocationRunning}, states,
		"the marker skip and real execution are kept, the plan is not repeated")
}

func TestNoisyHookStillRecordsItsOutcomeAfterOutputBudget(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "uid")
	rec := newTestRecorder(t, s, runID, nil)
	inv := NewID()
	rec.Invocation(
		Invocation{ID: inv, Phase: "lifecycle", Hook: hookCreate, State: InvocationRunning},
	)
	out := rec.Writer(inv, StreamStdout)
	chunk := strings.Repeat("n", MaxChunkBytes-1) + "\n"
	for range (MaxRunBytes/MaxChunkBytes)*2 + 64 {
		_, _ = out.Write([]byte(chunk))
	}
	require.NoError(t, out.Close())
	exit := 3
	rec.Invocation(Invocation{ID: inv, Hook: hookCreate, State: InvocationFailed, ExitCode: &exit})
	rec.Close(ProducerTerminal, true)

	require.NoError(t, rec.Err())
	m, err := s.Manifest(runID)
	require.NoError(t, err)
	assert.Contains(t, m.Conditions, ConditionCapacity)
	assert.Equal(t, ProducerTerminal, m.Producers[0].State)
	recs := readAll(t, s, ReadRequest{RunID: runID, Hook: hookCreate})
	last := recs[len(recs)-1]
	assert.Equal(t, KindInvocation, last.Kind)
	assert.Equal(t, InvocationFailed, last.State)
}
