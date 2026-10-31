package workspacelogs

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenRejectsRelativeRoot(t *testing.T) {
	_, err := Open(Options{Root: "relative/path"})
	require.ErrorIs(t, err, ErrUnsafePath)
}

func TestCreateRunValidatesAndJoins(t *testing.T) {
	s, _ := newTestStore(t)

	_, err := s.CreateRun(RunSpec{Command: "up"})
	require.ErrorIs(t, err, ErrInvalidRecord)
	_, err = s.CreateRun(RunSpec{RunID: "../../etc", Context: testContext, Command: "up"})
	require.ErrorIs(t, err, ErrInvalidID)

	first, err := s.CreateRun(RunSpec{Context: testContext, Command: "up", TaskID: "t1"})
	require.NoError(t, err)
	assert.Nil(t, first.WorkspaceUID)
	assert.Equal(t, CapturePending, first.CaptureState)

	joined, err := s.CreateRun(RunSpec{RunID: first.RunID, Context: testContext, Command: "up"})
	require.NoError(t, err)
	assert.Equal(t, first.RunID, joined.RunID)

	_, err = s.CreateRun(RunSpec{RunID: first.RunID, Context: "other", Command: "up"})
	require.ErrorIs(t, err, ErrIdentityConflict)
}

func TestBindWorkspaceOnce(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "")

	require.NoError(t, s.BindWorkspace(runID, "uid-1", "ws"))
	require.NoError(t, s.BindWorkspace(runID, "uid-1", "ws"), "same UID is idempotent")
	require.ErrorIs(t, s.BindWorkspace(runID, "uid-2", "ws"), ErrIdentityConflict)

	m, err := s.Manifest(runID)
	require.NoError(t, err)
	require.NotNil(t, m.WorkspaceUID)
	assert.Equal(t, "uid-1", *m.WorkspaceUID)
	assert.Equal(t, "ws", m.WorkspaceIDAtStart)
}

func TestAppendRequiresRegisteredProducer(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "u")

	_, err := s.Append([]Record{newSeq(runID, NewID()).output(NewID(), StreamStdout, "x")})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAppendDeduplicatesRetransmission(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "u")
	pid := newProducer(t, s, runID)
	seq := newSeq(runID, pid)
	inv := NewID()
	batch := []Record{
		seq.invocation(inv, hookCreate, InvocationRunning),
		seq.output(inv, StreamStdout, "one\n"),
		seq.output(inv, StreamStderr, "two\n"),
	}

	first := mustAppend(t, s, batch...)
	again := mustAppend(t, s, batch...)

	assert.Equal(t, 3, first.Written)
	assert.Equal(t, uint64(3), first.Accepted)
	assert.Equal(t, 3, again.Duplicates)
	assert.Zero(t, again.Written)
	assert.Equal(t, uint64(3), again.Accepted)
	assert.Equal(t, "one\ntwo\n", texts(readAll(t, s, ReadRequest{RunID: runID})))
}

func TestAppendRejectsGapWithoutAdvancing(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "u")
	pid := newProducer(t, s, runID)
	seq := newSeq(runID, pid)
	inv := NewID()
	mustAppend(t, s, seq.output(inv, StreamStdout, "a"))

	seq.next = 5
	res, err := s.Append([]Record{seq.output(inv, StreamStdout, "late")})

	require.ErrorIs(t, err, ErrSequenceGap)
	assert.Equal(t, uint64(1), res.Accepted)
}

func TestAppendRejectsInvalidBatches(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "u")
	pid := newProducer(t, s, runID)
	seq := newSeq(runID, pid)
	inv := NewID()

	tests := map[string][]Record{
		"oversized chunk": {seq.output(inv, StreamStdout, strings.Repeat("a", MaxChunkBytes+1))},
		"bad utf-8":       {seq.output(inv, StreamStdout, "\xff\xfe")},
		"bad stream":      {seq.output(inv, Stream("tty"), "x")},
		"unknown hook":    {seq.invocation(inv, "evilCommand", InvocationRunning)},
		"bad invocation":  {seq.invocation("not-a-uuid", hookCreate, InvocationRunning)},
	}
	for name, batch := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := s.Append(batch)
			require.Error(t, err)
		})
	}

	mixed := []Record{seq.output(inv, StreamStdout, "a"), seq.output(inv, StreamStdout, "b")}
	mixed[1].ProducerID = NewID()
	_, err := s.Append(mixed)
	require.ErrorIs(t, err, ErrInvalidRecord)
}

func TestAppendRollsSegmentsAndReadsAcrossThem(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "u")
	pid := newProducer(t, s, runID)
	seq := newSeq(runID, pid)
	inv := NewID()
	chunk := strings.Repeat("x", MaxChunkBytes)

	var want int
	for range 90 {
		mustAppend(t, s, seq.output(inv, StreamStdout, chunk))
		want += len(chunk)
	}

	dir, err := s.producerDir(runID, pid)
	require.NoError(t, err)
	segs, err := listSegments(dir)
	require.NoError(t, err)
	assert.Greater(t, len(segs), 1, "output past 512 KiB rolls to a new segment")
	assert.Len(t, texts(readAll(t, s, ReadRequest{RunID: runID, Limit: 17})), want)
}

func TestAppendRepairsPartialFinalRecord(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "u")
	pid := newProducer(t, s, runID)
	seq := newSeq(runID, pid)
	inv := NewID()
	mustAppend(t, s, seq.output(inv, StreamStdout, "kept\n"))

	dir, err := s.producerDir(runID, pid)
	require.NoError(t, err)
	segs, err := listSegments(dir)
	require.NoError(t, err)
	//nolint:gosec // test appends to its own temp dir
	f, err := os.OpenFile(filepath.Join(dir, segs[0].name), os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteString(`{"schemaVersion":1,"kind":"out`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	assert.Equal(t, "kept\n", texts(readAll(t, s, ReadRequest{RunID: runID})),
		"an uncommitted final line is not shown")

	// A fresh store instance models another process taking over the producer.
	other, err := Open(Options{Root: s.root})
	require.NoError(t, err)
	mustAppend(t, other, seq.output(inv, StreamStdout, "after\n"))
	assert.Equal(t, "kept\nafter\n", texts(readAll(t, other, ReadRequest{RunID: runID})))
}

func TestConcurrentStoresAppendWithoutLoss(t *testing.T) {
	root := t.TempDir()
	a, err := Open(Options{Root: root})
	require.NoError(t, err)
	b, err := Open(Options{Root: root})
	require.NoError(t, err)
	runID := newRun(t, a, "u")
	inv := NewID()

	var wg sync.WaitGroup
	for _, st := range []*Store{a, b} {
		pid := newProducer(t, st, runID)
		seq := newSeq(runID, pid)
		wg.Go(func() {
			for range 40 {
				_, appendErr := st.Append([]Record{seq.output(inv, StreamStdout, "line\n")})
				assert.NoError(t, appendErr)
			}
		})
	}
	wg.Wait()

	m, err := a.Manifest(runID)
	require.NoError(t, err)
	assert.Len(t, m.Producers, 2)
	assert.Equal(t, strings.Repeat("line\n", 80), texts(readAll(t, a, ReadRequest{RunID: runID})))
}

func TestStorageRejectsSymlinks(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "u")
	pid := newProducer(t, s, runID)
	outside := t.TempDir()

	runDir, err := s.runDir(runID)
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(filepath.Join(runDir, producersDir)))
	require.NoError(t, os.Symlink(outside, filepath.Join(runDir, producersDir)))

	_, err = s.Append([]Record{newSeq(runID, pid).output(NewID(), StreamStdout, "x")})
	require.ErrorIs(t, err, ErrUnsafePath)
	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	assert.Empty(t, entries, "nothing is written through the link")
}

func TestPermissionsArePrivate(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "u")
	pid := newProducer(t, s, runID)
	mustAppend(t, s, newSeq(runID, pid).output(NewID(), StreamStdout, "x"))

	if filepath.Separator == '\\' {
		t.Skip("POSIX permission bits do not apply")
	}
	require.NoError(t, filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
		require.NoError(t, err)
		info, infoErr := d.Info()
		require.NoError(t, infoErr)
		if path == s.root {
			return nil
		}
		if d.IsDir() {
			assert.Equal(t, os.FileMode(dirPerm), info.Mode().Perm(), path)
		} else {
			assert.Equal(t, os.FileMode(filePerm), info.Mode().Perm(), path)
		}

		return nil
	}))
}

func TestRunBudgetElidesOutputButKeepsSequenceAndMetadata(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "u")
	pid := newProducer(t, s, runID)
	seq := newSeq(runID, pid)
	inv := NewID()
	chunk := strings.Repeat("y", MaxChunkBytes)

	var elided int
	for range MaxRunBytes/MaxChunkBytes + 50 {
		elided += mustAppend(t, s, seq.output(inv, StreamStdout, chunk)).Elided
	}
	require.Positive(t, elided, "output past the budget is elided")

	mustAppend(t, s, seq.invocation(inv, hookCreate, InvocationFailed))
	mustAppend(t, s, seq.terminal(true))

	m, err := s.Manifest(runID)
	require.NoError(t, err)
	assert.Equal(t, CapturePartial, m.CaptureState)
	assert.Contains(t, m.Conditions, ConditionCapacity)
	assert.LessOrEqual(t, dirBytes(filepath.Join(s.root, runsDirName)), int64(MaxRunBytes))

	recs := readAll(t, s, ReadRequest{RunID: runID, Hook: hookCreate, Limit: MaxReadLimit})
	var failed bool
	for i := range recs {
		failed = failed || (recs[i].Kind == KindInvocation && recs[i].State == InvocationFailed)
	}
	assert.True(t, failed, "the terminal state survives a full output budget")
}

func TestCompleteNeedsSealedTerminalAndCleanCapture(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "u")
	pid := newProducer(t, s, runID)
	seq := newSeq(runID, pid)

	resp, err := s.Read(ReadRequest{RunID: runID})
	require.NoError(t, err)
	assert.False(t, resp.Complete, "a running producer is not complete")

	mustAppend(t, s, seq.terminal(false))
	resp, err = s.Read(ReadRequest{RunID: runID})
	require.NoError(t, err)
	assert.False(t, resp.Complete, "unsealed discovery is not complete")

	require.NoError(t, s.SealDiscovery(runID))
	resp, err = s.Read(ReadRequest{RunID: runID})
	require.NoError(t, err)
	assert.True(t, resp.Complete)
	assert.Equal(t, CaptureComplete, resp.Run.CaptureState)

	require.ErrorIs(
		t,
		s.RegisterProducer(runID, ProducerRef{ProducerID: NewID()}),
		ErrDiscoverySealed,
	)
}

func TestProducerCapMarksCapturePartial(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "u")
	for range MaxProducers {
		newProducer(t, s, runID)
	}

	err := s.RegisterProducer(runID, ProducerRef{ProducerID: NewID()})

	require.ErrorIs(t, err, ErrProducerCap)
	m, err := s.Manifest(runID)
	require.NoError(t, err)
	assert.Len(t, m.Producers, MaxProducers)
	assert.Equal(t, CapturePartial, m.CaptureState)
}
