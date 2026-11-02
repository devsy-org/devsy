package workspacelogs

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const otherContext = "other"

func makeDataDir(t *testing.T, perm os.FileMode) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	require.NoError(t, os.Mkdir(dir, perm))
	require.NoError(t, os.Chmod(dir, perm))

	return dir
}

func makeSpool(t *testing.T, dataDir string) string {
	t.Helper()
	root := filepath.Join(dataDir, SpoolDirName)
	require.NoError(t, os.Mkdir(root, 0o700))

	return root
}

func TestResolveSpoolFindsPrimaryThenFallback(t *testing.T) {
	primary := makeDataDir(t, 0o755)
	fallback := makeDataDir(t, 0o755)
	fallbackRoot := makeSpool(t, fallback)

	got, err := resolveSpoolIn([]string{primary, fallback})
	require.NoError(t, err)
	require.Equal(t, fallbackRoot, got)

	primaryRoot := makeSpool(t, primary)
	got, err = resolveSpoolIn([]string{primary, fallback})
	require.NoError(t, err)
	require.Equal(t, primaryRoot, got)
}

func TestResolveSpoolDoesNotCreateAnything(t *testing.T) {
	dir := makeDataDir(t, 0o755)

	_, err := resolveSpoolIn([]string{dir, filepath.Join(dir, "missing")})
	require.ErrorIs(t, err, ErrNotFound)

	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	require.Empty(t, entries)
}

func TestResolveSpoolRejectsSymlinkAndWritableByOthers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits and symlinks are not portable here")
	}
	dir := makeDataDir(t, 0o755)
	real := t.TempDir()
	require.NoError(t, os.Symlink(real, filepath.Join(dir, SpoolDirName)))

	_, err := resolveSpoolIn([]string{dir})
	require.ErrorIs(t, err, ErrUnsafePath)

	open := makeDataDir(t, 0o777)
	makeSpool(t, open)
	_, err = resolveSpoolIn([]string{open})
	require.ErrorIs(t, err, ErrUnsafePath)

	linkedData := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(real, linkedData))
	_, err = resolveSpoolIn([]string{linkedData})
	require.ErrorIs(t, err, ErrUnsafePath)
}

func TestUnavailableMarkerRoundTrip(t *testing.T) {
	reason, ok := ParseUnavailable("noise\n" + FormatUnavailable(ReasonSourceOffline) + "more")
	require.True(t, ok)
	require.Equal(t, ReasonSourceOffline, reason)

	_, ok = ParseUnavailable("plain error")
	require.False(t, ok)
	_, ok = ParseUnavailable(unavailableMarker + strings.Repeat("x", 400) + "\n")
	require.False(t, ok)
}

func spoolWithRun(t *testing.T, uid string) (root, runID string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), SpoolDirName)
	s, err := Open(Options{Root: root, Now: newClock().Now})
	require.NoError(t, err)
	runID = newRun(t, s, uid)
	producerID := newProducer(t, s, runID)
	seq := newSeq(runID, producerID)
	inv := NewID()
	mustAppend(
		t,
		s,
		seq.invocation(inv, hookCreate, InvocationRunning),
		seq.output(
			inv,
			StreamStdout,
			"hello\n",
		),
		seq.invocation(inv, hookCreate, InvocationSucceeded),
		seq.terminal(true),
	)

	return root, runID
}

func TestReadSpoolPageValidatesIdentity(t *testing.T) {
	root, runID := spoolWithRun(t, uidA)
	sel := SpoolSelector{
		Root: root, Context: testContext, WorkspaceUID: uidA,
		Read: ReadRequest{RunID: runID},
	}

	resp, err := ReadSpoolPage(sel)
	require.NoError(t, err)
	require.Equal(t, "hello\n", texts(resp.Records))

	for name, mutate := range map[string]func(*SpoolSelector){
		"context": func(s *SpoolSelector) { s.Context = otherContext },
		"uid":     func(s *SpoolSelector) { s.WorkspaceUID = "uid-b" },
		"empty uid": func(s *SpoolSelector) {
			s.WorkspaceUID = ""
		},
		"bad run": func(s *SpoolSelector) { s.Read.RunID = "../escape" },
	} {
		bad := sel
		mutate(&bad)
		_, err = ReadSpoolPage(bad)
		require.Error(t, err, name)
	}
}

func TestReadSpoolPageIsReadOnly(t *testing.T) {
	root, runID := spoolWithRun(t, uidA)
	before := readTree(t, root)
	entriesBefore := countFiles(t, root)

	_, err := ReadSpoolPage(SpoolSelector{
		Root: root, Context: testContext, WorkspaceUID: uidA, Read: ReadRequest{RunID: runID},
	})
	require.NoError(t, err)
	require.Equal(t, before, readTree(t, root))
	require.Equal(t, entriesBefore, countFiles(t, root))
}

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	require.NoError(t, filepath.WalkDir(dir, func(_ string, _ os.DirEntry, err error) error {
		n++

		return err
	}))

	return n
}

func TestDecodeReadResponseRejectsOversizedAndWrongSchema(t *testing.T) {
	_, err := DecodeReadResponse(bytes.NewReader(bytes.Repeat([]byte(" "), MaxPageBytes+1)))
	require.ErrorIs(t, err, ErrInvalidRecord)

	_, err = DecodeReadResponse(strings.NewReader(`{"schemaVersion":2}`))
	require.ErrorIs(t, err, ErrInvalidRecord)

	_, err = DecodeReadResponse(strings.NewReader(`not json`))
	require.ErrorIs(t, err, ErrInvalidRecord)

	resp, err := DecodeReadResponse(strings.NewReader(`{"schemaVersion":1,"records":[]}`))
	require.NoError(t, err)
	require.Empty(t, resp.Records)
}
