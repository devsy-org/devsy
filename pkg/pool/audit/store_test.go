//go:build !windows

package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/stretchr/testify/suite"
)

const (
	dayOne = "2026-11-22"
	dayTwo = "2026-11-23"
)

type StoreSuite struct {
	suite.Suite
	dir   string
	clock time.Time
}

func TestStoreSuite(t *testing.T) {
	suite.Run(t, new(StoreSuite))
}

func (s *StoreSuite) SetupTest() {
	s.dir = filepath.Join(s.T().TempDir(), "audit")
	west := time.FixedZone("west", -8*60*60)
	s.clock = time.Date(2026, 11, 22, 15, 59, 0, 0, west)
}

func (s *StoreSuite) TestAppendWritesOneLinePerRecord() {
	st := s.open()
	s.Require().NoError(st.Append([]byte(`{"a":1}`)))
	s.Require().NoError(st.Append([]byte(`{"b":"x y"}`)))
	s.Equal("{\"a\":1}\n{\"b\":\"x y\"}\n", s.read(dayOne))
	info, err := os.Stat(filepath.Join(s.dir, dayOne+fileExt))
	s.Require().NoError(err)
	s.Equal(os.FileMode(fileMode), info.Mode().Perm())
}

func (s *StoreSuite) TestRotatesAtUTCMidnight() {
	st := s.open()
	s.Require().NoError(st.Append([]byte(`{"n":1}`)))
	s.clock = s.clock.Add(2 * time.Minute)
	s.Require().NoError(st.Append([]byte(`{"n":2}`)))
	s.Equal("{\"n\":1}\n", s.read(dayOne))
	s.Equal("{\"n\":2}\n", s.read(dayTwo))
	days, err := st.Days()
	s.Require().NoError(err)
	s.Equal([]string{dayOne, dayTwo}, days)
}

func (s *StoreSuite) TestAppendCutsTornTail() {
	st := s.open()
	path := filepath.Join(s.dir, dayOne+fileExt)
	s.Require().NoError(os.WriteFile(path, []byte("{\"a\":1}\n{\"bad\":"), fileMode))
	s.Require().NoError(st.Append([]byte(`{"good":1}`)))
	s.Equal("{\"a\":1}\n{\"good\":1}\n", s.read(dayOne))
	s.Require().NoError(os.WriteFile(path, []byte("{\"bad\":"), fileMode))
	s.Require().NoError(st.Append([]byte(`{"good":2}`)))
	s.Equal("{\"good\":2}\n", s.read(dayOne))
}

func (s *StoreSuite) TestAppendRefusesHugeUnterminatedTail() {
	st := s.open()
	tail := bytes.Repeat([]byte("x"), MaxLineBytes+10)
	path := filepath.Join(s.dir, dayOne+fileExt)
	s.Require().NoError(os.WriteFile(path, tail, fileMode))
	s.Require().ErrorIs(st.Append([]byte(`{"a":1}`)), errTornTail)
	s.Equal(string(tail), s.read(dayOne))
}

func (s *StoreSuite) TestTornTailBoundary() {
	st := s.open()
	path := filepath.Join(s.dir, dayOne+fileExt)
	over := bytes.Repeat([]byte("x"), MaxLineBytes+1)
	s.Require().NoError(os.WriteFile(path, over, fileMode))
	s.Require().ErrorIs(st.Append([]byte(`{"a":1}`)), errTornTail)
	s.Equal(string(over), s.read(dayOne))
	s.ErrorIs(st.Scan("", "", func(string, []byte) error { return nil }), errTornTail)
	atLimit := bytes.Repeat([]byte("x"), MaxLineBytes)
	s.Require().NoError(os.WriteFile(path, atLimit, fileMode))
	s.Require().NoError(st.Append([]byte(`{"a":1}`)))
	s.Equal("{\"a\":1}\n", s.read(dayOne))
}

func (s *StoreSuite) TestAppendWaitsForTheDirectoryLock() {
	st := s.open()
	lock := flock.New(filepath.Join(s.dir, lockName), flock.SetPermissions(fileMode))
	s.Require().NoError(lock.Lock())
	defer func() { _ = lock.Unlock() }()
	done := make(chan error, 1)
	go func() { done <- st.Append([]byte(`{"a":1}`)) }()
	select {
	case <-done:
		s.FailNow("append finished while another holder had the lock")
	case <-time.After(200 * time.Millisecond):
	}
	s.Require().NoError(lock.Unlock())
	s.Require().NoError(<-done)
	s.Equal("{\"a\":1}\n", s.read(dayOne))
}

func (s *StoreSuite) TestAppendLockWaitIsBounded() {
	st := s.open()
	st.lockWait = 50 * time.Millisecond
	lock := flock.New(filepath.Join(s.dir, lockName), flock.SetPermissions(fileMode))
	s.Require().NoError(lock.Lock())
	defer func() { _ = lock.Unlock() }()
	start := time.Now()
	err := st.Append([]byte(`{"a":1}`))
	s.Require().ErrorIs(err, errLockTimeout)
	s.ErrorContains(err, "waited 50ms")
	s.Less(time.Since(start), 5*time.Second)
	_, statErr := os.Stat(filepath.Join(s.dir, dayOne+fileExt))
	s.True(os.IsNotExist(statErr), "a timed out append must not create a file")
	err = st.Scan("", "", func(string, []byte) error { return nil })
	s.NoError(err, "a shared lock waits only for exclusive holders, and no day file exists yet")
}

func (s *StoreSuite) TestScanLockWaitIsBounded() {
	st := s.open()
	s.Require().NoError(st.Append([]byte(`{"a":1}`)))
	st.lockWait = 50 * time.Millisecond
	lock := flock.New(filepath.Join(s.dir, lockName), flock.SetPermissions(fileMode))
	s.Require().NoError(lock.Lock())
	defer func() { _ = lock.Unlock() }()
	err := st.Scan("", "", func(string, []byte) error { return nil })
	s.ErrorIs(err, errLockTimeout)
	s.ErrorContains(err, "waited 50ms")
	s.ErrorContains(err, dayOne)
}

func (s *StoreSuite) TestScanErrorsNameTheDay() {
	st := s.open()
	s.Require().NoError(st.Append([]byte(`{"a":1}`)))
	s.clock = s.clock.Add(time.Minute)
	s.Require().NoError(os.WriteFile(
		filepath.Join(s.dir, dayTwo+fileExt), []byte("{\"a\":1}\n{\"b\""), fileMode))
	err := st.Scan("", "", func(string, []byte) error { return nil })
	s.Require().ErrorIs(err, io.ErrUnexpectedEOF)
	s.ErrorContains(err, dayTwo)
	s.NotContains(err.Error(), dayOne)
}

func (s *StoreSuite) TestEveryAppendSyncsTheDirectory() {
	st := s.open()
	calls := recordSyncs(s.T())
	for i := range 3 {
		s.Require().NoError(st.Append([]byte(fmt.Sprintf(`{"n":%d}`, i))))
	}
	s.Equal([]string{s.dir, s.dir, s.dir}, *calls)
}

func (s *StoreSuite) TestAppendSyncsAfterAnotherStoreFailedToSync() {
	first := s.open()
	s.Require().NoError(first.Append([]byte(`{"n":1}`)))
	s.Require().NoError(os.Remove(filepath.Join(s.dir, dayOne+fileExt)))
	second := s.open()
	calls := failSyncOnce(s.T())
	s.Require().ErrorContains(second.Append([]byte(`{"n":2}`)), "injected sync failure")
	s.Require().NoError(first.Append([]byte(`{"n":3}`)))
	s.Equal([]string{s.dir, s.dir}, *calls, "the older store must not trust its own earlier sync")
}

func (s *StoreSuite) TestOpenCreatesNestedDirectories() {
	nested := filepath.Join(s.T().TempDir(), "a", "b", "audit")
	_, err := Open(nested)
	s.Require().NoError(err)
	info, err := os.Stat(nested)
	s.Require().NoError(err)
	s.True(info.IsDir())
	_, err = Open(nested)
	s.NoError(err)
}

func (s *StoreSuite) TestScanRefusesNonRegularDayFile() {
	st := s.open()
	target := filepath.Join(s.T().TempDir(), "target")
	s.Require().NoError(os.WriteFile(target, []byte("{\"a\":1}\n"), fileMode))
	s.Require().NoError(os.Symlink(target, filepath.Join(s.dir, dayOne+fileExt)))
	s.NoError(st.Scan(dayOne, dayOne, func(string, []byte) error { return nil }),
		"Days skips a symlink, so Scan never opens it")
	_, err := st.openSnapshot(dayOne)
	s.Error(err, "a symlinked day file must not be opened")
}

func (s *StoreSuite) TestScanCallbackMayAppend() {
	st := s.open()
	s.Require().NoError(st.Append([]byte(`{"n":1}`)))
	done := make(chan error, 1)
	go func() {
		done <- st.Scan("", "", func(string, []byte) error {
			return st.Append([]byte(`{"n":2}`))
		})
	}()
	select {
	case err := <-done:
		s.Require().NoError(err)
	case <-time.After(5 * time.Second):
		s.FailNow("scan callback blocked an append")
	}
	s.Equal("{\"n\":1}\n{\"n\":2}\n", s.read(dayOne))
}

func (s *StoreSuite) TestScanStopsAtItsSnapshot() {
	st := s.open()
	s.Require().NoError(st.Append([]byte(`{"n":1}`)))
	var got []string
	s.Require().NoError(st.Scan("", "", func(_ string, rec []byte) error {
		got = append(got, string(rec))
		return st.Append([]byte(`{"n":2}`))
	}))
	s.Equal([]string{`{"n":1}`}, got)
}

func (s *StoreSuite) TestScanRefusesOversizedLine() {
	st := s.open()
	line := bytes.Repeat([]byte("x"), MaxLineBytes+10)
	s.Require().
		NoError(os.WriteFile(filepath.Join(s.dir, dayOne+fileExt), append(line, '\n'), fileMode))
	s.ErrorIs(st.Scan("", "", func(string, []byte) error { return nil }), errLineTooLong)
}

func (s *StoreSuite) TestScanRefusesHugeUnterminatedTail() {
	st := s.open()
	tail := bytes.Repeat([]byte("x"), MaxLineBytes+10)
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, dayOne+fileExt), tail, fileMode))
	s.ErrorIs(st.Scan("", "", func(string, []byte) error { return nil }), errTornTail)
}

func (s *StoreSuite) TestScanRefusesSymlinkedLock() {
	st := s.open()
	s.Require().NoError(st.Append([]byte(`{"a":1}`)))
	target := filepath.Join(s.T().TempDir(), "target")
	s.Require().NoError(os.WriteFile(target, nil, fileMode))
	s.Require().NoError(os.Remove(filepath.Join(s.dir, lockName)))
	s.Require().NoError(os.Symlink(target, filepath.Join(s.dir, lockName)))
	s.Error(st.Scan("", "", func(string, []byte) error { return nil }))
}

func (s *StoreSuite) TestScanLineLimitIsExact() {
	st := s.open()
	path := filepath.Join(s.dir, dayOne+fileExt)
	pad := func(n int) []byte {
		return []byte(`{"a":"` + strings.Repeat("x", n-len(`{"a":""}`)) + `"}`)
	}
	atLimit := append(pad(MaxLineBytes), '\n')
	s.Require().NoError(os.WriteFile(path, atLimit, fileMode))
	s.NoError(st.Scan("", "", func(string, []byte) error { return nil }))
	over := append(pad(MaxLineBytes+1), '\n')
	s.Require().NoError(os.WriteFile(path, over, fileMode))
	s.ErrorIs(st.Scan("", "", func(string, []byte) error { return nil }), errLineTooLong)
}

func (s *StoreSuite) TestScanKeepsRepairedTailOutOfSnapshot() {
	st := s.open()
	path := filepath.Join(s.dir, dayOne+fileExt)
	// A maximum-size record forces the reader to refill after the callback,
	// which is when a repaired tail would show up in a physical-size snapshot.
	first := `{"a":"` + strings.Repeat("x", MaxLineBytes-len(`{"a":""}`)) + `"}`
	s.Require().NoError(os.WriteFile(path, []byte(first+"\n{\"torn\":\"xxxxxxxxxxxx"), fileMode))
	var got []string
	err := st.Scan("", "", func(_ string, rec []byte) error {
		got = append(got, string(rec))
		return st.Append([]byte(`{"n":1}`))
	})
	s.ErrorIs(err, io.ErrUnexpectedEOF)
	s.Equal([]string{first}, got)
}

func (s *StoreSuite) TestSymlinksAreRefused() {
	st := s.open()
	target := filepath.Join(s.T().TempDir(), "target")
	s.Require().NoError(os.WriteFile(target, nil, fileMode))
	s.Require().NoError(os.Symlink(target, filepath.Join(s.dir, dayOne+fileExt)))
	s.Error(st.Append([]byte(`{"a":1}`)))
	s.Require().NoError(os.Remove(filepath.Join(s.dir, dayOne+fileExt)))
	s.Require().NoError(os.Remove(filepath.Join(s.dir, lockName)))
	s.Require().NoError(os.Symlink(target, filepath.Join(s.dir, lockName)))
	s.Error(st.Append([]byte(`{"a":1}`)))
	info, err := os.Stat(target)
	s.Require().NoError(err)
	s.Zero(info.Size(), "a symlinked path must not be written through")
}

func (s *StoreSuite) TestRefusesBadRecords() {
	st := s.open()
	cases := map[string][]byte{
		"empty":     nil,
		"newline":   []byte("{\"a\":1}\n{\"b\":2}"),
		"cr":        []byte("{\"a\":\r1}"),
		"array":     []byte("[1]"),
		"scalar":    []byte("1"),
		"truncated": []byte(`{"a":`),
		"too long":  []byte(`{"a":"` + strings.Repeat("x", MaxLineBytes) + `"}`),
	}
	for name, record := range cases {
		s.Error(st.Append(record), name)
	}
	_, err := os.Stat(filepath.Join(s.dir, dayOne+fileExt))
	s.True(os.IsNotExist(err), "a refused record must not create a file")
}

func (s *StoreSuite) TestWriteFailureIsAnError() {
	st := s.open()
	s.Require().NoError(os.Mkdir(filepath.Join(s.dir, dayOne+fileExt), dirMode))
	s.Error(st.Append([]byte(`{"a":1}`)))
}

func (s *StoreSuite) TestConcurrentWritersDoNotInterleave() {
	const writers, perWriter = 8, 20
	pad := strings.Repeat("p", 4096)
	var wg sync.WaitGroup
	for w := range writers {
		wg.Go(func() {
			st := s.open()
			// The default wait is an operator limit, not a test limit: a loaded
			// disk can make eight writers queue behind each other's fsync.
			st.lockWait = time.Minute
			for i := range perWriter {
				rec := fmt.Sprintf(`{"w":%d,"i":%d,"pad":"%s"}`, w, i, pad)
				s.NoError(st.Append([]byte(rec)))
			}
		})
	}
	wg.Wait()
	seen := map[[2]int]bool{}
	for line := range strings.SplitSeq(strings.TrimSuffix(s.read(dayOne), "\n"), "\n") {
		var rec struct{ W, I int }
		s.Require().NoError(json.Unmarshal([]byte(line), &rec), "interleaved line")
		seen[[2]int{rec.W, rec.I}] = true
	}
	s.Len(seen, writers*perWriter)
}

func (s *StoreSuite) TestScanOrderAndBounds() {
	st := s.open()
	s.Require().NoError(st.Append([]byte(`{"n":1}`)))
	s.clock = s.clock.Add(time.Minute)
	s.Require().NoError(st.Append([]byte(`{"n":2}`)))
	s.Require().NoError(st.Append([]byte(`{"n":3}`)))
	collect := func(from, to string) []string {
		var out []string
		s.Require().NoError(st.Scan(from, to, func(day string, rec []byte) error {
			out = append(out, day+" "+string(rec))
			return nil
		}))
		return out
	}
	s.Equal(
		[]string{dayOne + " {\"n\":1}", dayTwo + " {\"n\":2}", dayTwo + " {\"n\":3}"},
		collect("", ""),
	)
	s.Equal([]string{dayTwo + " {\"n\":2}", dayTwo + " {\"n\":3}"}, collect(dayTwo, ""))
	s.Equal([]string{dayOne + " {\"n\":1}"}, collect("", dayOne))
	stop := fmt.Errorf("stop")
	s.ErrorIs(st.Scan("", "", func(string, []byte) error { return stop }), stop)
}

func (s *StoreSuite) TestScanReportsPartialLine() {
	st := s.open()
	s.Require().NoError(os.WriteFile(
		filepath.Join(s.dir, dayOne+fileExt), []byte("{\"a\":1}\n{\"b\""), fileMode))
	var got []string
	err := st.Scan("", "", func(_ string, rec []byte) error {
		got = append(got, string(rec))
		return nil
	})
	s.ErrorIs(err, io.ErrUnexpectedEOF)
	s.Equal([]string{"{\"a\":1}"}, got)
}

func (s *StoreSuite) TestDaysIgnoresOtherFiles() {
	st := s.open()
	for _, name := range []string{".lock", "notes.txt", "2026-11-22.jsonl.bak", "2026-11-22.jsonl"} {
		s.Require().NoError(os.WriteFile(filepath.Join(s.dir, name), nil, fileMode))
	}
	days, err := st.Days()
	s.Require().NoError(err)
	s.Equal([]string{dayOne}, days)
}

func (s *StoreSuite) TestOpenRefusesWritableDirectory() {
	s.Require().NoError(os.MkdirAll(s.dir, dirMode))
	s.Require().NoError(os.Chmod(s.dir, dirMode|0o070))
	_, err := Open(s.dir)
	s.ErrorContains(err, "must not be writable")
}

func (s *StoreSuite) TestHostIDIsMintedOnceAndKept() {
	path := filepath.Join(s.T().TempDir(), "host-id")
	first, err := LoadOrCreateHostID(path)
	s.Require().NoError(err)
	s.Regexp(hostIDPattern, first)
	again, err := LoadOrCreateHostID(path)
	s.Require().NoError(err)
	s.Equal(first, again)
}

func (s *StoreSuite) TestHostIDFileIsBounded() {
	path := filepath.Join(s.T().TempDir(), "host-id")
	s.Require().NoError(os.WriteFile(path, bytes.Repeat([]byte("a"), 1<<20), fileMode))
	_, err := LoadOrCreateHostID(path)
	s.Error(err)
}

func (s *StoreSuite) TestHostIDPaddedPastLimitRefused() {
	path := filepath.Join(s.T().TempDir(), "host-id")
	id := hostIDPrefix + NewID()
	s.Require().NoError(os.WriteFile(path, []byte(id+strings.Repeat(" ", 200)+"CANARY"), fileMode))
	_, err := LoadOrCreateHostID(path)
	s.Require().Error(err)
	s.NotContains(err.Error(), "CANARY")
}

func (s *StoreSuite) TestHostIDNeverReplaced() {
	path := filepath.Join(s.T().TempDir(), "host-id")
	s.Require().NoError(os.WriteFile(path, []byte("CANARY"), fileMode))
	_, err := LoadOrCreateHostID(path)
	s.Require().Error(err)
	s.NotContains(err.Error(), "CANARY")
	data, err := os.ReadFile(filepath.Clean(path))
	s.Require().NoError(err)
	s.True(bytes.Equal([]byte("CANARY"), data))
}

func (s *StoreSuite) TestHostIDEmptyOrPartialFileIsRepaired() {
	cases := map[string]string{
		"empty":      "",
		"blank":      " \n",
		"nuls":       "\x00\x00\x00\x00",
		"one char":   "h",
		"prefix":     "h_",
		"half":       "h_01ARZ3NDEKTSV",
		"cut by one": "h_01ARZ3NDEKTSV4RRFFQ69G5FA",
	}
	for name, content := range cases {
		path := filepath.Join(s.T().TempDir(), "host-id")
		s.Require().NoError(os.WriteFile(path, []byte(content), fileMode), name)
		id, repaired, err := loadOrCreateHostID(path)
		s.Require().NoError(err, name)
		s.True(repaired, name)
		s.Regexp(hostIDPattern, id, name)
		again, repaired, err := loadOrCreateHostID(path)
		s.Require().NoError(err, name)
		s.False(repaired, name)
		s.Equal(id, again, name)
	}
}

func (s *StoreSuite) TestHostIDPartialDoesNotMatchOtherContent() {
	others := []string{"x", "H_", "h_01arz", "h_01ARZ3NDEKTSV4RRFFQ69G5FAVX"}
	for _, content := range others {
		if hostIDPattern.MatchString(strings.TrimSpace(content)) {
			continue
		}
		path := filepath.Join(s.T().TempDir(), "host-id")
		s.Require().NoError(os.WriteFile(path, []byte(content), fileMode))
		_, err := LoadOrCreateHostID(path)
		s.Error(err, content)
		data, readErr := os.ReadFile(filepath.Clean(path))
		s.Require().NoError(readErr)
		s.Equal(content, string(data), "other content must be left alone")
	}
}

func (s *StoreSuite) TestHostIDPublishLeavesNoTemporaryFiles() {
	dir := s.T().TempDir()
	_, err := LoadOrCreateHostID(filepath.Join(dir, "host-id"))
	s.Require().NoError(err)
	entries, err := os.ReadDir(dir)
	s.Require().NoError(err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	s.ElementsMatch([]string{"host-id", "host-id" + hostIDLockSuffix}, names)
}

func (s *StoreSuite) TestOpenUsesTheDefaultLockWait() {
	st := s.open()
	s.Equal(5*time.Second, defaultLockWait)
	s.Equal(defaultLockWait, st.lockWait)
}

func (s *StoreSuite) TestHostIDLeavesOtherFilesAlone() {
	dir := s.T().TempDir()
	path := filepath.Join(dir, "host-id")
	const sibling = "h_01ARZ3NDEKTSV4RRFFQ69G5FAV\n"
	names := []string{
		".host-id-tmp-f75e587e8440e822-123",
		".host-id-host-id-123",
		".host-id-123",
		".host-id-keep",
		"notes",
	}
	for _, name := range names {
		s.Require().NoError(os.WriteFile(filepath.Join(dir, name), []byte(sibling), fileMode))
	}
	_, err := LoadOrCreateHostID(path)
	s.Require().NoError(err)
	_, err = LoadOrCreateHostID(path)
	s.Require().NoError(err)
	for _, name := range names {
		data, readErr := os.ReadFile(filepath.Clean(filepath.Join(dir, name)))
		s.Require().NoError(readErr)
		s.Equal(sibling, string(data), "%s must be left alone", name)
	}
}

func (s *StoreSuite) TestHostIDStagesNextToTheIDFileAndRemovesIt() {
	dir := s.T().TempDir()
	path := filepath.Join(dir, "host-id")
	var staged string
	orig := renameFunc
	s.T().Cleanup(func() { renameFunc = orig })
	renameFunc = func(oldpath, _ string) error {
		staged = oldpath
		return errors.New("injected rename failure")
	}
	_, err := LoadOrCreateHostID(path)
	s.Require().ErrorContains(err, "injected rename failure")
	s.Equal(dir, filepath.Dir(staged), "the checked directory is the only staging place")
	s.True(strings.HasPrefix(filepath.Base(staged), ".host-id-"))
	s.NoFileExists(staged, "a failed publish removes the file it created")
	s.NoFileExists(path)
}

func (s *StoreSuite) TestHostIDLongBaseNameIsAccepted() {
	dir := s.T().TempDir()
	path := filepath.Join(dir, strings.Repeat("h", 240))
	id, err := LoadOrCreateHostID(path)
	s.Require().NoError(err)
	s.Regexp(hostIDPattern, id)
}

func (s *StoreSuite) TestHostIDRefusesSymlinkedLock() {
	dir := s.T().TempDir()
	target := filepath.Join(s.T().TempDir(), "target")
	s.Require().NoError(os.WriteFile(target, nil, fileMode))
	s.Require().NoError(os.Symlink(target, filepath.Join(dir, "host-id"+hostIDLockSuffix)))
	_, err := LoadOrCreateHostID(filepath.Join(dir, "host-id"))
	s.Error(err)
}

const lockHolderEnv = "AUDIT_TEST_LOCK_HOLDER"

// childCommand re-runs this test binary, so the command is never input.
var childCommand = exec.Command

// TestHostIDLockHolderHelper is the child half of the cross-process lock
// test: it holds the lock named by the environment until its stdin closes.
func TestHostIDLockHolderHelper(t *testing.T) {
	path := os.Getenv(lockHolderEnv)
	if path == "" {
		t.Skip("runs only as a child of the lock test")
	}
	lock, err := newFlock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := acquire(lock, defaultLockWait, false); err != nil {
		t.Fatal(err)
	}
	_, _ = os.Stdout.WriteString("locked\n")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func (s *StoreSuite) TestHostIDRefusesLockNamesAndKeepsTheLockExclusive() {
	dir := s.T().TempDir()
	path := filepath.Join(dir, "host-id")
	lockPath := path + hostIDLockSuffix
	s.Require().NoError(os.WriteFile(lockPath, nil, fileMode))
	release := s.holdLockInChild(lockPath)
	before, err := os.Stat(lockPath)
	s.Require().NoError(err)

	for _, name := range []string{"host-id.lock", "host-id.LOCK", "host-id.loc\u212a", ".lock"} {
		_, err := LoadOrCreateHostID(filepath.Join(dir, name))
		s.ErrorIs(err, errReservedHostIDName, name)
	}

	after, err := os.Stat(lockPath)
	s.Require().NoError(err)
	s.True(os.SameFile(before, after), "the lock file must stay the same inode")
	data, err := os.ReadFile(filepath.Clean(lockPath))
	s.Require().NoError(err)
	s.Empty(data, "the lock file must not be written")
	entries, err := os.ReadDir(dir)
	s.Require().NoError(err)
	s.Len(entries, 1, "a refused name must not create files")

	contender, err := newFlock(lockPath)
	s.Require().NoError(err)
	s.Error(acquire(contender, 100*time.Millisecond, false), "the child still holds the lock")
	release()
	s.Require().NoError(acquire(contender, defaultLockWait, false))
	s.Require().NoError(contender.Close())

	id, err := LoadOrCreateHostID(path)
	s.Require().NoError(err)
	s.Regexp(hostIDPattern, id)
}

func (s *StoreSuite) TestHostIDConcurrentRepairAgrees() {
	path := filepath.Join(s.T().TempDir(), "host-id")
	s.Require().NoError(os.WriteFile(path, nil, fileMode))
	ids := make([]string, 8)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Go(func() {
			id, err := LoadOrCreateHostID(path)
			s.NoError(err)
			ids[i] = id
		})
	}
	wg.Wait()
	for _, id := range ids {
		s.Equal(ids[0], id)
	}
}

func (s *StoreSuite) TestHostIDConcurrentFirstUseAgrees() {
	path := filepath.Join(s.T().TempDir(), "host-id")
	ids := make([]string, 8)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Go(func() {
			id, err := LoadOrCreateHostID(path)
			s.NoError(err)
			ids[i] = id
		})
	}
	wg.Wait()
	for _, id := range ids {
		s.Equal(ids[0], id)
	}
}

func (s *StoreSuite) TestNewIDIsULID() {
	seen := map[string]bool{}
	for range 1000 {
		id := NewID()
		s.Len(id, 26)
		s.False(seen[id], "ids must not repeat")
		seen[id] = true
	}
}

// failSyncOnce makes the next directory sync fail and records every call.
func failSyncOnce(t *testing.T) *[]string {
	var calls []string
	failed := false
	orig := syncDirFunc
	t.Cleanup(func() { syncDirFunc = orig })
	syncDirFunc = func(dir string) error {
		calls = append(calls, dir)
		if !failed {
			failed = true
			return errors.New("injected sync failure")
		}
		return nil
	}
	return &calls
}

func recordSyncs(t *testing.T) *[]string {
	var calls []string
	orig := syncDirFunc
	t.Cleanup(func() { syncDirFunc = orig })
	syncDirFunc = func(dir string) error {
		calls = append(calls, dir)
		return nil
	}
	return &calls
}

func (s *StoreSuite) TestOpenSyncsTheDirectoryChain() {
	calls := recordSyncs(s.T())
	root := s.T().TempDir()
	nested := filepath.Join(root, "a", "b", "audit")
	_, err := Open(nested)
	s.Require().NoError(err)
	s.Contains(*calls, nested)
	s.Contains(*calls, filepath.Join(root, "a", "b"))
	s.Contains(*calls, filepath.Join(root, "a"))
	s.Contains(*calls, root)
}

func (s *StoreSuite) TestOpenRetriesSyncAfterAFailure() {
	calls := failSyncOnce(s.T())
	_, err := Open(s.dir)
	s.Require().ErrorContains(err, "injected sync failure")
	before := len(*calls)
	_, err = Open(s.dir)
	s.Require().NoError(err, "the existing directory must be synced again")
	s.Greater(len(*calls), before)
}

func (s *StoreSuite) TestAppendRetriesDirectorySyncAfterAFailure() {
	st := s.open()
	calls := failSyncOnce(s.T())
	s.Require().ErrorContains(st.Append([]byte(`{"n":1}`)), "injected sync failure")
	s.Require().NoError(st.Append([]byte(`{"n":2}`)))
	s.Equal([]string{s.dir, s.dir}, *calls)
}

func (s *StoreSuite) TestRecreatedDayFileIsSyncedAgain() {
	st := s.open()
	calls := recordSyncs(s.T())
	s.Require().NoError(st.Append([]byte(`{"n":1}`)))
	s.Require().NoError(os.Remove(filepath.Join(s.dir, dayOne+fileExt)))
	s.Require().NoError(st.Append([]byte(`{"n":2}`)))
	s.Len(*calls, 2)
}

func (s *StoreSuite) TestHostIDRetriesDirectorySyncAfterAFailure() {
	dir := s.T().TempDir()
	path := filepath.Join(dir, "host-id")
	calls := failSyncOnce(s.T())
	_, err := LoadOrCreateHostID(path)
	s.Require().ErrorContains(err, "injected sync failure")
	id, err := LoadOrCreateHostID(path)
	s.Require().NoError(err)
	s.Regexp(hostIDPattern, id)
	s.Equal([]string{dir, dir}, *calls, "the retry must sync before it returns the ID")
}

func (s *StoreSuite) TestHostIDBlankAndPrefixShapes() {
	repaired := []string{"\f", "\v", "\u00a0", " \x00\n", "h_7", "h_0123456789"}
	for _, content := range repaired {
		path := filepath.Join(s.T().TempDir(), "host-id")
		s.Require().NoError(os.WriteFile(path, []byte(content), fileMode))
		_, wasRepaired, err := loadOrCreateHostID(path)
		s.Require().NoError(err, "%q", content)
		s.True(wasRepaired, "%q", content)
	}
	for _, content := range []string{"h_Z", "h_8", "h_8ZZZ", "hh", "h_0I"} {
		path := filepath.Join(s.T().TempDir(), "host-id")
		s.Require().NoError(os.WriteFile(path, []byte(content), fileMode))
		_, err := LoadOrCreateHostID(path)
		s.Error(err, "%q", content)
		data, readErr := os.ReadFile(filepath.Clean(path))
		s.Require().NoError(readErr)
		s.Equal(content, string(data))
	}
}

func (s *StoreSuite) open() *Store {
	st, err := Open(s.dir)
	s.Require().NoError(err)
	st.now = func() time.Time { return s.clock }
	return st
}

func (s *StoreSuite) read(day string) string {
	data, err := os.ReadFile(filepath.Clean(filepath.Join(s.dir, day+fileExt)))
	s.Require().NoError(err)
	return string(data)
}

// holdLockInChild starts a separate process that holds the flock at path and
// returns a function that releases it and waits for the process to exit.
func (s *StoreSuite) holdLockInChild(path string) (release func()) {
	executable, err := os.Executable()
	s.Require().NoError(err)
	cmd := childCommand(executable, "-test.run=^TestHostIDLockHolderHelper$")
	cmd.Env = append(os.Environ(), lockHolderEnv+"="+path)
	stdin, err := cmd.StdinPipe()
	s.Require().NoError(err)
	stdout, err := cmd.StdoutPipe()
	s.Require().NoError(err)
	s.Require().NoError(cmd.Start())
	var once sync.Once
	stop := func(kill bool) {
		once.Do(func() {
			_ = stdin.Close()
			if kill {
				_ = cmd.Process.Kill()
			}
			if err := cmd.Wait(); !kill {
				s.NoError(err, "the child exits cleanly when released")
			}
		})
	}
	s.T().Cleanup(func() { stop(true) })
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		ready <- line
	}()
	select {
	case line := <-ready:
		s.Require().Equal("locked\n", line)
	case <-time.After(30 * time.Second):
		s.FailNow("child did not take the lock")
	}
	return func() { stop(false) }
}
