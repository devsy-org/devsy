package workspacelogs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runExists(t *testing.T, s *Store, runID string) bool {
	t.Helper()
	_, err := s.Manifest(runID)
	if err == nil {
		return true
	}
	require.ErrorIs(t, err, ErrNotFound)

	return false
}

func TestPruneKeepsNewestCompletedRunsPerGeneration(t *testing.T) {
	s, _ := newTestStore(t)
	var old []string
	for range MaxCompletedRuns + 3 {
		id := newRun(t, s, uidA)
		finishRun(t, s, id)
		old = append(old, id)
	}
	other := newRun(t, s, "uid-b")
	finishRun(t, s, other)

	require.NoError(t, s.Prune())

	for i, id := range old {
		assert.Equal(t, i >= 3, runExists(t, s, id), "run %d", i)
	}
	assert.True(t, runExists(t, s, other), "another generation is counted separately")
}

func TestPruneNeverRemovesActiveRuns(t *testing.T) {
	s, _ := newTestStore(t)
	active := newRun(t, s, uidA)
	newProducer(t, s, active)
	for range MaxCompletedRuns + 2 {
		finishRun(t, s, newRun(t, s, uidA))
	}

	require.NoError(t, s.Prune())

	assert.True(t, runExists(t, s, active))
}

func TestPruneRemovesExpiredAndStaleActiveRuns(t *testing.T) {
	s, clock := newTestStore(t)
	done := newRun(t, s, uidA)
	finishRun(t, s, done)
	crashed := newRun(t, s, uidA)
	newProducer(t, s, crashed)

	clock.Advance(RetentionAge + time.Hour)
	fresh := newRun(t, s, uidA)
	newProducer(t, s, fresh)
	require.NoError(t, s.Prune())

	assert.False(t, runExists(t, s, done), "completed run past retention age")
	assert.False(
		t,
		runExists(t, s, crashed),
		"a writer idle past retention age is treated as crashed",
	)
	assert.True(t, runExists(t, s, fresh))
}

func TestPruneEnforcesGenerationByteCap(t *testing.T) {
	s, _ := newTestStore(t)
	chunk := strings.Repeat("b", MaxChunkBytes)
	var ids []string
	for range 7 {
		id := newRun(t, s, uidA)
		pid := newProducer(t, s, id)
		seq := newSeq(id, pid)
		inv := NewID()
		for range MaxRunBytes/MaxChunkBytes - 40 {
			mustAppend(t, s, seq.output(inv, StreamStdout, chunk))
		}
		mustAppend(t, s, seq.terminal(true))
		ids = append(ids, id)
	}

	require.NoError(t, s.Prune())

	var kept int
	for _, id := range ids {
		if runExists(t, s, id) {
			kept++
		}
	}
	assert.Less(t, kept, len(ids))
	assert.LessOrEqual(t, dirBytes(s.root), int64(MaxGenerationBytes))
	assert.True(t, runExists(t, s, ids[len(ids)-1]), "the newest run survives")
}

func TestOpenPrunesExpiredRuns(t *testing.T) {
	s, clock := newTestStore(t)
	id := newRun(t, s, uidA)
	finishRun(t, s, id)

	clock.Advance(RetentionAge + time.Hour)
	reopened, err := Open(Options{Root: s.root, Now: clock.Now})
	require.NoError(t, err)

	assert.False(t, runExists(t, reopened, id))
}

func TestPruneIgnoresForeignEntries(t *testing.T) {
	s, _ := newTestStore(t)
	require.NoError(t, s.ensureDir(runsDirName, "not-a-run-id"))
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, s.root+"/"+runsDirName+"/"+NewID()))

	require.NoError(t, s.Prune())

	_, err := os.Stat(outside)
	require.NoError(t, err, "pruning never follows a link out of the store")
}

func TestPruneRemovesAcknowledgementFilesOfPrunedRuns(t *testing.T) {
	s, _ := newTestStore(t)
	acks, err := NewAckStore(filepath.Join(s.Root(), AckDirName))
	require.NoError(t, err)
	var ids []string
	for range MaxCompletedRuns + 1 {
		id := newRun(t, s, uidA)
		finishRun(t, s, id)
		require.NoError(t, acks.Advance(AckKey{RunID: id, ProducerID: NewID(), Hop: HopInbound}, 3))
		ids = append(ids, id)
	}

	require.NoError(t, s.Prune())

	left, err := filepath.Glob(filepath.Join(s.Root(), AckDirName, ids[0]+".*.json"))
	require.NoError(t, err)
	assert.Empty(t, left)
	kept, err := filepath.Glob(filepath.Join(s.Root(), AckDirName, ids[len(ids)-1]+".*.json"))
	require.NoError(t, err)
	assert.Len(t, kept, 1)
}
