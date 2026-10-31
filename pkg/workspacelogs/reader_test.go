package workspacelogs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hookRun builds a run with a silent postCreate, a noisy postStart and a
// second producer, all closed.
type hookRun struct {
	runID, producerID, silent, noisy string
}

func newHookRun(t *testing.T, s *Store) hookRun {
	t.Helper()
	runID := newRun(t, s, "uid")
	pid := newProducer(t, s, runID)
	seq := newSeq(runID, pid)
	h := hookRun{runID: runID, producerID: pid, silent: NewID(), noisy: NewID()}

	mustAppend(t, s,
		seq.invocation(h.silent, hookCreate, InvocationRunning),
		seq.invocation(h.silent, hookCreate, InvocationSucceeded),
		seq.invocation(h.noisy, "postStartCommand", InvocationRunning),
		seq.output(h.noisy, StreamStdout, "out-1\n"),
		seq.output(h.noisy, StreamStderr, "err-1\n"),
		seq.output(h.noisy, StreamStdout, "out-2\n"),
		seq.invocation(h.noisy, "postStartCommand", InvocationFailed),
		seq.terminal(true),
	)

	return h
}

func TestReadPagesByLimitWithoutDuplicates(t *testing.T) {
	s, _ := newTestStore(t)
	h := newHookRun(t, s)

	first, err := s.Read(ReadRequest{RunID: h.runID, Limit: 3})
	require.NoError(t, err)
	assert.Len(t, first.Records, 3)
	assert.True(t, first.HasMore)
	assert.False(t, first.Complete, "complete only once every page is drained")

	all := readAll(t, s, ReadRequest{RunID: h.runID, Limit: 3})
	assert.Len(t, all, 8)
	seen := map[uint64]bool{}
	for i := range all {
		assert.False(t, seen[all[i].Sequence], "sequence %d repeated", all[i].Sequence)
		seen[all[i].Sequence] = true
	}

	last, err := s.Read(ReadRequest{RunID: h.runID})
	require.NoError(t, err)
	assert.True(t, last.Complete)
	assert.False(t, last.HasMore)
}

func TestReadHonorsByteCap(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "uid")
	pid := newProducer(t, s, runID)
	seq := newSeq(runID, pid)
	inv := NewID()
	for range 40 {
		mustAppend(t, s, seq.output(inv, StreamStdout, strings.Repeat("z", MaxChunkBytes)))
	}

	resp, err := s.Read(ReadRequest{RunID: runID, MaxBytes: 32 * 1024})
	require.NoError(t, err)
	assert.True(t, resp.HasMore)
	assert.LessOrEqual(t, len(resp.Records), 4)
	assert.Len(
		t,
		texts(readAll(t, s, ReadRequest{RunID: runID, MaxBytes: 32 * 1024})),
		40*MaxChunkBytes,
	)

	_, err = s.Read(ReadRequest{RunID: runID, MaxBytes: MaxPageBytes + 1})
	require.ErrorIs(t, err, ErrInvalidSelector)
	_, err = s.Read(ReadRequest{RunID: runID, Limit: MaxReadLimit + 1})
	require.ErrorIs(t, err, ErrInvalidSelector)
}

func TestReadFiltersByHookAndInvocation(t *testing.T) {
	s, _ := newTestStore(t)
	h := newHookRun(t, s)

	noisy := readAll(t, s, ReadRequest{RunID: h.runID, Hook: "postStartCommand"})
	assert.Equal(t, "out-1\nerr-1\nout-2\n", texts(noisy))
	for i := range noisy {
		assert.NotEqual(t, KindProducer, noisy[i].Kind)
	}

	silent := readAll(t, s, ReadRequest{RunID: h.runID, Hook: hookCreate})
	assert.Empty(t, texts(silent))
	require.NotEmpty(t, silent, "a silent hook still returns its transitions")
	assert.Equal(t, InvocationSucceeded, silent[len(silent)-1].State)

	byID := readAll(t, s, ReadRequest{RunID: h.runID, InvocationID: h.noisy})
	assert.Equal(t, "out-1\nerr-1\nout-2\n", texts(byID))
}

func TestReadRejectsBadSelectors(t *testing.T) {
	s, _ := newTestStore(t)
	h := newHookRun(t, s)
	other := newHookRun(t, s)

	_, err := s.Read(ReadRequest{RunID: h.runID, Hook: "rm -rf"})
	require.ErrorIs(t, err, ErrInvalidSelector)
	_, err = s.Read(ReadRequest{RunID: h.runID, InvocationID: "x"})
	require.ErrorIs(t, err, ErrInvalidID)
	_, err = s.Read(ReadRequest{RunID: h.runID, InvocationID: other.noisy})
	require.ErrorIs(t, err, ErrNotFound, "an invocation from another run is a lookup error")
	_, err = s.Read(ReadRequest{RunID: "../x"})
	require.ErrorIs(t, err, ErrInvalidID)
	_, err = s.Read(ReadRequest{RunID: NewID()})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestCursorBindsRunAndFilters(t *testing.T) {
	s, _ := newTestStore(t)
	h := newHookRun(t, s)
	other := newHookRun(t, s)

	page, err := s.Read(ReadRequest{RunID: h.runID, Limit: 2})
	require.NoError(t, err)

	_, err = s.Read(ReadRequest{RunID: other.runID, After: page.NextCursor})
	require.ErrorIs(t, err, ErrInvalidCursor)
	_, err = s.Read(ReadRequest{RunID: h.runID, Hook: "postStartCommand", After: page.NextCursor})
	require.ErrorIs(t, err, ErrInvalidCursor)
	_, err = s.Read(ReadRequest{RunID: h.runID, After: "garbage"})
	require.ErrorIs(t, err, ErrInvalidCursor)
	_, err = s.Read(ReadRequest{RunID: h.runID, After: strings.Repeat("a", MaxCursorBytes+1)})
	require.ErrorIs(t, err, ErrInvalidCursor)

	indexCursor := indexCursorFor(t, s)
	_, err = s.Read(ReadRequest{RunID: h.runID, After: indexCursor})
	require.ErrorIs(t, err, ErrInvalidCursor, "an index cursor is not a run cursor")
}

func indexCursorFor(t *testing.T, s *Store) string {
	t.Helper()
	resp, err := s.ListRuns(IndexRequest{Context: testContext, Limit: 1})
	require.NoError(t, err)
	require.NotEmpty(t, resp.NextCursor)

	return resp.NextCursor
}

func TestReadReportsRetentionGap(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "uid")
	pid := newProducer(t, s, runID)
	seq := newSeq(runID, pid)
	inv := NewID()
	// Three segments of 60 records each, then drop the middle one.
	chunk := strings.Repeat("g", MaxChunkBytes)
	for range 190 {
		mustAppend(t, s, seq.output(inv, StreamStdout, chunk))
	}
	dir, err := s.producerDir(runID, pid)
	require.NoError(t, err)
	segs, err := listSegments(dir)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(segs), 3)
	require.NoError(t, os.Remove(filepath.Join(dir, segs[1].name)))

	recs := readAll(t, s, ReadRequest{RunID: runID, Limit: MaxReadLimit})

	var gap *Record
	for i := range recs {
		if recs[i].Condition == ConditionRetentionGap {
			gap = &recs[i]
		}
	}
	require.NotNil(t, gap)
	assert.Equal(t, pid, gap.ProducerID, "the gap names the affected producer")
	assert.Contains(t, gap.Detail, "are not retained")

	finishRun(t, s, runID)
	req := ReadRequest{RunID: runID, Limit: MaxReadLimit}
	for {
		resp, readErr := s.Read(req)
		require.NoError(t, readErr)
		if !resp.HasMore {
			assert.False(
				t,
				resp.Complete,
				"a gap seen on an earlier page keeps the read incomplete",
			)

			break
		}
		req.After = resp.NextCursor
	}
}

func TestReadSurfacesMalformedCommittedRecord(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "uid")
	pid := newProducer(t, s, runID)
	seq := newSeq(runID, pid)
	inv := NewID()
	good1 := marshalTestLine(t, seq.output(inv, StreamStdout, "good-1\n"))
	bad := []byte("not json\n")
	seq.next++
	good2 := marshalTestLine(t, seq.output(inv, StreamStdout, "good-2\n"))

	// Corrupt a committed line in the middle of the segment.
	require.NoError(t, s.ensureDir(runsDirName, runID, producersDir, pid))
	dir, err := s.producerDir(runID, pid)
	require.NoError(t, err)
	content := append(append(good1, bad...), good2...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, segmentName(1)), content, filePerm))

	recs := readAll(t, s, ReadRequest{RunID: runID, Limit: 1})

	assert.Equal(t, "good-1\ngood-2\n", texts(recs))
	var malformed int
	for i := range recs {
		if recs[i].Condition == ConditionMalformedRecord {
			malformed++
		}
	}
	assert.Equal(t, 1, malformed, "reported once, not on every page")
}

func marshalTestLine(t *testing.T, rec Record) []byte {
	t.Helper()
	line, err := marshalLine(rec)
	require.NoError(t, err)

	return line
}

func TestReadPageResumesAcrossProducers(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "uid")
	inv := NewID()
	for _, label := range []string{"a", "b"} {
		pid := newProducer(t, s, runID)
		seq := newSeq(runID, pid)
		mustAppend(
			t,
			s,
			seq.output(inv, StreamStdout, label+"1"),
			seq.output(inv, StreamStdout, label+"2"),
		)
	}

	assert.Equal(t, "a1a2b1b2", texts(readAll(t, s, ReadRequest{RunID: runID, Limit: 1})))
}

func TestListRunsOrdersAndIsolates(t *testing.T) {
	s, _ := newTestStore(t)
	var ids []string
	for range 5 {
		ids = append(ids, newRun(t, s, uidA))
	}
	otherWorkspace := newRun(t, s, "uid-b")
	_, err := s.CreateRun(RunSpec{Context: "elsewhere", WorkspaceUID: uidA, Command: "up"})
	require.NoError(t, err)

	var got []string
	req := IndexRequest{Context: testContext, WorkspaceUID: uidA, Limit: 2}
	for {
		resp, listErr := s.ListRuns(req)
		require.NoError(t, listErr)
		for _, r := range resp.Runs {
			got = append(got, r.RunID)
		}
		if !resp.HasMore {
			break
		}
		req.After = resp.NextCursor
	}

	assert.Equal(t, []string{ids[4], ids[3], ids[2], ids[1], ids[0]}, got, "newest first")
	assert.NotContains(t, got, otherWorkspace)

	all, err := s.ListRuns(IndexRequest{Context: testContext, Limit: MaxIndexLimit})
	require.NoError(t, err)
	assert.Len(t, all.Runs, 6, "no workspace argument lists the whole context")
}

func TestListRunsRejectsChangedSelectorsAndLimits(t *testing.T) {
	s, _ := newTestStore(t)
	for range 3 {
		newRun(t, s, uidA)
	}
	page, err := s.ListRuns(IndexRequest{Context: testContext, WorkspaceUID: uidA, Limit: 1})
	require.NoError(t, err)
	require.NotEmpty(t, page.NextCursor)

	_, err = s.ListRuns(
		IndexRequest{Context: testContext, WorkspaceUID: "uid-b", After: page.NextCursor},
	)
	require.ErrorIs(t, err, ErrInvalidCursor)
	_, err = s.ListRuns(
		IndexRequest{Context: "other", WorkspaceUID: uidA, After: page.NextCursor},
	)
	require.ErrorIs(t, err, ErrInvalidCursor)
	_, err = s.ListRuns(IndexRequest{Context: testContext, Limit: MaxIndexLimit + 1})
	require.ErrorIs(t, err, ErrInvalidSelector)
	_, err = s.ListRuns(IndexRequest{})
	require.ErrorIs(t, err, ErrInvalidSelector)

	runCursor, err := (&cursor{Version: SchemaVersion, Kind: cursorRun, Run: NewID()}).encode()
	require.NoError(t, err)
	_, err = s.ListRuns(IndexRequest{Context: testContext, After: runCursor})
	require.ErrorIs(t, err, ErrInvalidCursor, "a run cursor is not an index cursor")
}

func TestRenamedWorkspaceKeepsRunAndNewUIDDoesNot(t *testing.T) {
	s, _ := newTestStore(t)
	m, err := s.CreateRun(RunSpec{
		Context: testContext, WorkspaceUID: "uid-old", WorkspaceIDAtStart: "app", Command: "up",
	})
	require.NoError(t, err)

	byUID, err := s.ListRuns(IndexRequest{Context: testContext, WorkspaceUID: "uid-old"})
	require.NoError(t, err)
	require.Len(t, byUID.Runs, 1, "lookup is by UID, so a rename does not lose the run")
	assert.Equal(t, m.RunID, byUID.Runs[0].RunID)

	reused, err := s.ListRuns(IndexRequest{Context: testContext, WorkspaceUID: "uid-new"})
	require.NoError(t, err)
	assert.Empty(t, reused.Runs, "a new workspace that reuses the name does not inherit old runs")
}

func TestSplitTextKeepsUTF8AndTrailingFragment(t *testing.T) {
	text := strings.Repeat("é", 10) + "tail"
	chunks := SplitText(text, 5)

	assert.Equal(t, text, strings.Join(chunks, ""))
	for _, c := range chunks {
		assert.LessOrEqual(t, len(c), 5)
		assert.True(t, utf8.ValidString(c), "chunk %q splits a rune", c)
	}
	assert.Equal(t, []string{"no newline"}, SplitText("no newline", MaxChunkBytes))
	assert.Empty(t, SplitText("", MaxChunkBytes))
}
