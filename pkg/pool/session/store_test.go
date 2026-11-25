//go:build !windows

package session

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

const storePool = "pool"

type StoreSuite struct {
	suite.Suite
	layout Layout
	lock   *Lock
	dir    string
	store  *Store
	clock  time.Time
}

func TestStoreSuite(t *testing.T) { suite.Run(t, new(StoreSuite)) }

func (s *StoreSuite) SetupTest() {
	s.layout = s.newLayout(storePool)
	lock, err := s.layout.Lock()
	s.Require().NoError(err)
	s.lock = lock
	s.T().Cleanup(func() { _ = lock.Release() })
	s.dir = s.layout.SessionsDir()
	s.clock = time.Date(2026, time.November, 25, 12, 0, 0, 0, time.UTC)
	s.store = s.open()
}

func (s *StoreSuite) TestCreateWritesAQueuedSessionWithOwnerOnlyModes() {
	sess := s.create()
	s.Equal(StateQueued, sess.State)
	s.Equal(storePool, sess.Pool)
	s.True(sess.Created.Equal(s.clock) && sess.Updated.Equal(s.clock))
	info, err := os.Stat(filepath.Join(s.dir, sess.ID+fileExt))
	s.Require().NoError(err)
	s.Equal(os.FileMode(fileMode), info.Mode().Perm())
	dirInfo, err := os.Stat(s.dir)
	s.Require().NoError(err)
	s.Equal(os.FileMode(dirMode), dirInfo.Mode().Perm())
	got, err := s.store.Load(sess.ID)
	s.Require().NoError(err)
	s.Equal(sess, got)
	s.Len(s.snapshot(), 1, "no temp file is left behind")
}

func (s *StoreSuite) TestCreateNeverReplacesAnExistingSession() {
	useSource(s.T(), fixedReader{0x00})
	first := s.create()
	before := s.snapshot()
	got, err := s.store.Create()
	s.Require().ErrorIs(err, ErrExists)
	s.Zero(got)
	s.Equal(before, s.snapshot())
	s.Equal("p-pool-aaaaaaaaaa", first.ID)
}

func (s *StoreSuite) TestCreateReturnsZeroSessionWhenEncodingFails() {
	before := s.poolSnapshot()
	s.clock = time.Time{}
	got, err := s.store.Create()
	s.Require().ErrorIs(err, errInvalidSession)
	s.Zero(got)
	s.Equal(before, s.poolSnapshot())
}

func (s *StoreSuite) TestTransitionWritesTheNewStateAndKeepsCreated() {
	sess := s.create()
	s.clock = s.clock.Add(time.Minute)
	next, err := s.store.Transition(sess.ID, StateQueued, StateSpawning, "")
	s.Require().NoError(err)
	s.Equal(StateSpawning, next.State)
	s.True(next.Created.Equal(sess.Created))
	s.True(next.Updated.Equal(s.clock))
	got, err := s.store.Load(sess.ID)
	s.Require().NoError(err)
	s.Equal(next, got)
	_, err = s.store.Transition(sess.ID, StateSpawning, StateFailed, "spawn_timeout")
	s.Require().NoError(err)
	got, err = s.store.Load(sess.ID)
	s.Require().NoError(err)
	s.Equal("spawn_timeout", got.Reason)
}

func (s *StoreSuite) TestRefusedTransitionsLeaveTheFileByteIdentical() {
	sess := s.create()
	before := s.snapshot()
	cases := map[string]func() error{
		"illegal edge": func() error {
			_, err := s.store.Transition(sess.ID, StateQueued, StateBusy, "")
			return err
		},
		"wrong from": func() error {
			_, err := s.store.Transition(sess.ID, StateReady, StateBusy, "")
			return err
		},
		"bad reason": func() error {
			_, err := s.store.Transition(sess.ID, StateQueued, StateSpawning, "free text")
			return err
		},
	}
	want := map[string]error{
		"illegal edge": ErrIllegalTransition,
		"wrong from":   ErrStateConflict,
		"bad reason":   errInvalidSession,
	}
	for name, run := range cases {
		s.Require().ErrorIs(run(), want[name], name)
		s.Equal(before, s.snapshot(), name)
	}
}

func (s *StoreSuite) TestFailedPublishKeepsTheOldFileAndLeavesNoTempFile() {
	sess := s.create()
	before := s.snapshot()
	s.stubRename(func(*os.Root, string, string) error { return errors.New("disk full") })
	next, err := s.store.Transition(sess.ID, StateQueued, StateSpawning, "")
	s.Require().ErrorContains(err, "disk full")
	s.Zero(next)
	s.Equal(before, s.snapshot())
	got, err := s.store.Load(sess.ID)
	s.Require().NoError(err)
	s.Equal(StateQueued, got.State)
}

func (s *StoreSuite) TestMissingAndInvalidIDs() {
	_, err := s.store.Load("p-pool-aaaaaaaaaa")
	s.Require().ErrorIs(err, ErrNotFound)
	_, err = s.store.Transition("p-pool-aaaaaaaaaa", StateQueued, StateSpawning, "")
	s.Require().ErrorIs(err, ErrNotFound)
	for _, id := range []string{"", "../x", "p-pool-aaaaaaaaaa/../x", "p-pool-AAAAAAAAAA"} {
		_, err = s.store.Load(id)
		s.Require().ErrorIs(err, errInvalidID, id)
		_, err = s.store.Transition(id, StateQueued, StateSpawning, "")
		s.Require().ErrorIs(err, errInvalidID, id)
	}
}

func (s *StoreSuite) TestTornTempFilesAreIgnored() {
	s.writeFile(".tmp-123", `{"v":1,"id":"p-po`)
	sess := s.create()
	list, err := s.store.List()
	s.Require().NoError(err)
	s.Require().Len(list, 1)
	s.Equal(sess, list[0])
	_, err = s.store.Transition(sess.ID, StateQueued, StateSpawning, "")
	s.Require().NoError(err)
}

func (s *StoreSuite) TestListReturnsEverySessionInIDOrderAcrossReopen() {
	useSource(s.T(), fixedReader{0x00})
	first := s.create()
	useSource(s.T(), fixedReader{0xff})
	second := s.create()
	list, err := s.open().List()
	s.Require().NoError(err)
	s.Equal([]Session{second, first}, list, "7 sorts before a")
}

func (s *StoreSuite) TestListStopsOnFilesThatAreNotValidSessions() {
	good := s.create()
	const canary = "private-canary-918273"
	other := "p-other-aaaaaaaaaa"
	for name, file := range map[string][2]string{
		"garbage":    {"p-pool-bbbbbbbbbb.json", `{"canary":"` + canary + `"}`},
		"stray":      {"notes.txt", "x"},
		"other pool": {other + fileExt, mustEncode(s, other, "other")},
	} {
		path := s.writeFile(file[0], file[1])
		_, err := s.store.List()
		s.Require().Error(err, name)
		s.NotContains(err.Error(), canary, name)
		s.Require().NoError(os.Remove(path))
	}
	list, err := s.store.List()
	s.Require().NoError(err)
	s.Equal([]Session{good}, list)
}

func mustEncode(s *StoreSuite, id, pool string) string {
	sess := validSession()
	sess.ID, sess.Pool = id, pool
	data, err := encode(sess)
	s.Require().NoError(err)
	return string(data)
}

func (s *StoreSuite) TestLoadRefusesUnsafeFiles() {
	sess := s.create()
	path := filepath.Join(s.dir, sess.ID+fileExt)
	valid, err := os.ReadFile(filepath.Clean(path))
	s.Require().NoError(err)
	otherID := "p-pool-bbbbbbbbbb"
	s.writeFile(otherID+fileExt, string(valid))
	_, err = s.store.Load(otherID)
	s.Require().ErrorIs(err, errInvalidSession, "file name must match the ID inside")

	big := "p-pool-cccccccccc"
	s.writeFile(big+fileExt, string(bytes.Repeat([]byte(" "), maxFileBytes+1)))
	_, err = s.store.Load(big)
	s.Require().ErrorIs(err, errTooLarge)

	link := "p-pool-dddddddddd"
	s.Require().NoError(os.Symlink(path, filepath.Join(s.dir, link+fileExt)))
	_, err = s.store.Load(link)
	s.Require().Error(err, "an absolute link is refused")
	s.NotErrorIs(err, ErrNotFound)

	inside := "p-pool-gggggggggg"
	s.Require().NoError(os.Symlink(sess.ID+fileExt, filepath.Join(s.dir, inside+fileExt)))
	_, err = s.store.Load(inside)
	s.Require().ErrorIs(err, errNotRegular, "a link to a file in the same directory is refused")

	fifo := "p-pool-eeeeeeeeee"
	s.Require().NoError(syscall.Mkfifo(filepath.Join(s.dir, fifo+fileExt), fileMode))
	_, err = s.store.Load(fifo)
	s.Require().ErrorIs(err, errNotRegular)

	s.writeFile("p-pool-ffffffffff.json", string(valid[:len(valid)-1])+`,"extra":1}`)
	_, err = s.store.Load("p-pool-ffffffffff")
	s.Require().ErrorIs(err, errInvalidSession)
}

func (s *StoreSuite) TestOpenStoreChecksThePoolAndTheDirectory() {
	_, err := OpenStore(s.dir, "Bad Pool", s.lock)
	s.Require().ErrorIs(err, errInvalidPool)

	for _, extra := range []os.FileMode{0o070, 0o050, 0o005, 0o040, 0o001} {
		s.Require().NoError(os.Chmod(s.dir, dirMode|extra))
		_, err = OpenStore(s.dir, storePool, s.lock)
		s.Require().ErrorContains(err, "accessible only by its owner", "%o", extra)
	}
	s.Require().NoError(os.Chmod(s.dir, dirMode))

	s.Require().NoError(os.RemoveAll(s.dir))
	fresh, err := OpenStore(s.dir, storePool, s.lock)
	s.Require().NoError(err)
	s.Require().NoError(fresh.Close())
	info, err := os.Stat(s.dir)
	s.Require().NoError(err)
	s.Equal(os.FileMode(dirMode), info.Mode().Perm())
}

func (s *StoreSuite) TestOpenStoreRefusesALockThatDoesNotCoverTheDirectory() {
	elsewhere := s.newLayout(storePool)
	foreign, err := elsewhere.Lock()
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = foreign.Release() })
	spare := s.newLayout("spare")
	released, err := spare.Lock()
	s.Require().NoError(err)
	s.Require().NoError(released.Release())
	s.Require().NoError(os.RemoveAll(s.dir))
	s.Require().NoError(os.Remove(s.layout.KeysDir()))

	cases := map[string]struct {
		dir, pool string
		lock      *Lock
		want      error
	}{
		"nil":            {s.dir, storePool, nil, errLockNotHeld},
		"released":       {spare.SessionsDir(), "spare", released, errLockNotHeld},
		"other pool dir": {elsewhere.SessionsDir(), storePool, s.lock, errForeignLock},
		"foreign lock":   {s.dir, storePool, foreign, errForeignLock},
		"other pool":     {s.dir, "other", s.lock, errForeignLock},
		"not sessions":   {s.layout.KeysDir(), storePool, s.lock, errForeignLock},
		"not a child": {
			filepath.Join(s.dir, "sub", sessionsName),
			storePool,
			s.lock,
			errForeignLock,
		},
		"the pool dir": {s.layout.Dir(), storePool, s.lock, errForeignLock},
	}
	for name, tc := range cases {
		_, err := OpenStore(tc.dir, tc.pool, tc.lock)
		s.Require().ErrorIs(err, tc.want, name)
		if tc.dir != s.layout.Dir() {
			s.NoDirExists(tc.dir, name)
		}
	}
	s.Equal([]string{lockName}, s.poolNames(), "a refusal changes nothing in the pool directory")
}

func (s *StoreSuite) TestOpenStoreRefusesASessionsLinkToAnotherPool() {
	elsewhere := s.newLayout(storePool)
	foreign, err := elsewhere.Lock()
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = foreign.Release() })
	other, err := OpenStore(elsewhere.SessionsDir(), storePool, foreign)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = other.Close() })
	kept, err := other.Create()
	s.Require().NoError(err)
	before := dirNames(s, elsewhere.SessionsDir())

	s.Require().NoError(os.RemoveAll(s.dir))
	s.Require().NoError(os.Symlink(elsewhere.SessionsDir(), s.dir))
	_, err = OpenStore(s.dir, storePool, s.lock)
	s.Require().ErrorIs(err, errNotDirectory)
	s.Equal(before, dirNames(s, elsewhere.SessionsDir()))
	got, err := other.Load(kept.ID)
	s.Require().NoError(err)
	s.Equal(kept, got)
}

func (s *StoreSuite) TestWritesStopWhenThePoolPathLeadsElsewhere() {
	sess := s.create()
	moved := s.layout.Dir() + "-moved"
	s.Require().NoError(os.Rename(s.layout.Dir(), moved))
	replacement, err := s.layout.Lock()
	s.Require().NoError(err, "the moved directory's lock file is a different file")
	s.T().Cleanup(func() { _ = replacement.Release() })

	_, err = s.store.Create()
	s.Require().ErrorIs(err, errLockNotHeld)
	_, err = s.store.Transition(sess.ID, StateQueued, StateSpawning, "")
	s.Require().ErrorIs(err, errLockNotHeld)
	_, err = OpenStore(s.dir, storePool, s.lock)
	s.Require().ErrorIs(err, errLockNotHeld)

	s.Equal([]string{lockName}, s.poolNames(), "the replacement pool is untouched")
	s.Equal([]string{sess.ID + fileExt}, dirNames(s, filepath.Join(moved, sessionsName)))
	fresh, err := OpenStore(s.dir, storePool, replacement)
	s.Require().NoError(err)
	s.Require().NoError(fresh.Close())
}

func (s *StoreSuite) TestWritesStopWhenThePoolLockFileIsReplaced() {
	path := filepath.Join(s.layout.Dir(), lockName)
	s.Require().NoError(os.Remove(path))
	_, err := s.store.Create()
	s.Require().ErrorIs(err, errLockNotHeld, "removed")
	s.Require().NoError(os.WriteFile(path, nil, fileMode))
	_, err = s.store.Create()
	s.Require().ErrorIs(err, errLockNotHeld, "replaced by another file")
	s.Empty(dirNames(s, s.dir))
}

func (s *StoreSuite) TestReleaseKeepsTheOSLockUntilAWriteInFlightFinishes() {
	layout := s.newLayout(storePool)
	lock, err := layout.Lock()
	s.Require().NoError(err)
	store, err := OpenStore(layout.SessionsDir(), storePool, lock)
	s.Require().NoError(err)
	sess, err := store.Create()
	s.Require().NoError(err)

	entered, proceed := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(proceed) }) }
	writer := make(chan error, 1)
	released := make(chan error, 1)
	s.T().Cleanup(func() {
		finish()
		waitFor(writer, released)
	})
	s.stubRename(func(root *os.Root, oldName, newName string) error {
		close(entered)
		<-proceed
		return root.Rename(oldName, newName)
	})
	go func() {
		_, err := store.Transition(sess.ID, StateQueued, StateSpawning, "")
		writer <- err
	}()
	s.waitSignal(entered, "the write never reached the rename")
	go func() { released <- lock.Release() }()
	for range 20 {
		other, err := layout.Lock()
		if err == nil {
			_ = other.Release()
		}
		s.Require().ErrorIs(err, ErrPoolLocked, "another owner must wait for the write")
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-released:
		s.Fail("Release returned before the write finished")
	default:
	}
	finish()
	s.Require().NoError(s.receive(writer, "the write never finished"))
	s.Require().NoError(s.receive(released, "Release never returned"))
	again, err := layout.Lock()
	s.Require().NoError(err)
	s.Require().NoError(again.Release())
	s.Require().NoError(store.Close())
}

// waitFor gives a stuck goroutine a few seconds to finish and then leaves it
// behind, so a deadlock fails one test and does not hang the package.
func waitFor(chs ...<-chan error) {
	deadline := time.After(5 * time.Second)
	for _, ch := range chs {
		select {
		case <-ch:
		case <-deadline:
			return
		}
	}
}

func (s *StoreSuite) TestOpenStoreRefusesASessionsLinkInsideThePool() {
	s.Require().NoError(os.Rename(s.dir, filepath.Join(s.layout.Dir(), "real")))
	s.Require().NoError(os.Symlink("real", s.dir))
	_, err := OpenStore(s.dir, storePool, s.lock)
	s.Require().ErrorIs(err, errNotDirectory)
}

func (s *StoreSuite) TestStoreStopsWhenItsDirectoryMovesToAnotherPool() {
	sess := s.create()
	elsewhere := s.newLayout(storePool)
	foreign, err := elsewhere.Lock()
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = foreign.Release() })
	s.Require().NoError(os.Rename(s.dir, elsewhere.SessionsDir()))
	before := dirNames(s, elsewhere.SessionsDir())

	_, err = s.store.Create()
	s.Require().ErrorIs(err, errStoreMoved)
	_, err = s.store.Transition(sess.ID, StateQueued, StateSpawning, "")
	s.Require().ErrorIs(err, errStoreMoved)
	_, err = s.store.Load(sess.ID)
	s.Require().ErrorIs(err, errStoreMoved)
	_, err = s.store.List()
	s.Require().ErrorIs(err, errStoreMoved)
	s.Equal(before, dirNames(s, elsewhere.SessionsDir()))
	s.NoDirExists(s.dir)
}

func (s *StoreSuite) TestReadsNeverFailWhileSessionsAreTransitioning() {
	sess := s.create()
	path := []State{StateSpawning, StateReady, StateBusy}
	for range 100 {
		path = append(path, StateIdle, StateBusy)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		from := StateQueued
		for _, to := range path {
			_, err := s.store.Transition(sess.ID, from, to, "")
			s.NoError(err)
			from = to
		}
	})
	for range 3 {
		wg.Go(func() {
			for range 300 {
				_, err := s.store.Load(sess.ID)
				s.NoError(err)
				_, err = s.store.List()
				s.NoError(err)
			}
		})
	}
	wg.Wait()
}

func (s *StoreSuite) TestTheLockIsHeldOnTheFileInThePinnedDirectory() {
	held, err := s.lock.file.Stat()
	s.Require().NoError(err)
	entry, err := os.Stat(filepath.Join(s.layout.Dir(), lockName))
	s.Require().NoError(err)
	s.True(os.SameFile(held, entry))
}

func (s *StoreSuite) TestWritesStopWhenTheLockIsReleased() {
	sess := s.create()
	before := s.snapshot()
	s.Require().NoError(s.lock.Release())
	_, err := s.store.Create()
	s.Require().ErrorIs(err, errLockNotHeld)
	_, err = s.store.Transition(sess.ID, StateQueued, StateSpawning, "")
	s.Require().ErrorIs(err, errLockNotHeld)
	s.Equal(before, s.snapshot())
	_, err = s.store.Load(sess.ID)
	s.Require().ErrorIs(err, errLockNotHeld)
	_, err = s.store.List()
	s.Require().ErrorIs(err, errLockNotHeld)
	_, err = OpenStore(s.dir, storePool, s.lock)
	s.Require().ErrorIs(err, errLockNotHeld)
}

func (s *StoreSuite) TestReleaseWaitsForWritesInFlightAndStopsTheRest() {
	var wg sync.WaitGroup
	var once sync.Once
	started := make(chan struct{})
	errs := make(chan error, 200)
	for range 4 {
		wg.Go(func() {
			for range 50 {
				_, err := s.store.Create()
				errs <- err
				once.Do(func() { close(started) })
			}
		})
	}
	<-started
	s.Require().NoError(s.lock.Release())
	frozen := s.snapshot()
	wg.Wait()
	close(errs)
	s.Equal(frozen, s.snapshot(), "nothing is written after Release returns")
	for err := range errs {
		if err != nil {
			s.Require().ErrorIs(err, errLockNotHeld)
		}
	}
}

func (s *StoreSuite) TestOpenStoreRemovesStrayTempFilesOnly() {
	keep := s.create()
	s.writeFile(".tmp-1", "torn")
	s.writeFile(".tmp-4294967295", "torn")
	s.writeFile(".tmp-abc", "not ours")
	s.writeFile(".tmp-12a", "not ours")
	target := filepath.Join(s.dir, keep.ID+fileExt)
	s.Require().NoError(os.Symlink(target, filepath.Join(s.dir, ".tmp-7")))
	s.Require().NoError(os.Mkdir(filepath.Join(s.dir, ".tmp-8"), dirMode))

	s.open()

	s.ElementsMatch(
		[]string{keep.ID + fileExt, ".tmp-abc", ".tmp-12a", ".tmp-7", ".tmp-8"},
		s.names(),
	)
	_, err := s.store.List()
	s.Require().ErrorIs(err, errUnexpectedEntry, "foreign names are refused")
}

func (s *StoreSuite) TestOpenStoreDoesNotRemoveAWritersTempFile() {
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 200 {
			_, err := s.store.Create()
			s.NoError(err)
		}
	})
	for range 200 {
		s.open()
	}
	wg.Wait()
	list, err := s.store.List()
	s.Require().NoError(err)
	s.Len(list, 200)
}

func (s *StoreSuite) TestRacingTransitionsHaveOneWinner() {
	sess := s.create()
	var wg sync.WaitGroup
	results := make(chan error, 16)
	for range 16 {
		wg.Go(func() {
			_, err := s.store.Transition(sess.ID, StateQueued, StateSpawning, "")
			results <- err
		})
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrStateConflict):
			conflicts++
		default:
			s.Failf("unexpected error", "%v", err)
		}
	}
	s.Equal(1, wins)
	s.Equal(15, conflicts)
}

func (s *StoreSuite) TestTwoStoresOnOneDirectoryTakeTurns() {
	for range 30 {
		sess := s.create()
		other := s.open()
		var wg sync.WaitGroup
		results := make(chan error, 8)
		for i := range 8 {
			store, to := s.store, StateSpawning
			if i%2 == 1 {
				store, to = other, StateReclaiming
			}
			wg.Go(func() {
				_, err := store.Transition(sess.ID, StateQueued, to, "")
				results <- err
			})
		}
		wg.Wait()
		close(results)
		wins := 0
		for err := range results {
			if err == nil {
				wins++
			} else {
				s.Require().ErrorIs(err, ErrStateConflict)
			}
		}
		s.Require().Equal(1, wins)
	}
}

func (s *StoreSuite) TestConcurrentCreatesMakeDistinctSessions() {
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			_, err := s.store.Create()
			s.NoError(err)
		})
	}
	wg.Wait()
	list, err := s.store.List()
	s.Require().NoError(err)
	s.Len(list, 40, fmt.Sprint(len(s.snapshot())))
}

func (s *StoreSuite) waitSignal(ch <-chan struct{}, msg string) {
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		s.FailNow(msg)
	}
}

func (s *StoreSuite) receive(ch <-chan error, msg string) error {
	select {
	case err := <-ch:
		return err
	case <-time.After(10 * time.Second):
		s.FailNow(msg)
		return nil
	}
}

func (s *StoreSuite) newLayout(pool string) Layout {
	l, err := NewLayout(filepath.Join(s.T().TempDir(), "pools"), pool)
	s.Require().NoError(err)
	return l
}

func (s *StoreSuite) names() []string { return dirNames(s, s.dir) }

func (s *StoreSuite) poolNames() []string { return dirNames(s, s.layout.Dir()) }

func dirNames(s *StoreSuite, dir string) []string {
	entries, err := os.ReadDir(dir)
	s.Require().NoError(err)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func (s *StoreSuite) stubRename(stub func(*os.Root, string, string) error) {
	orig := renameFile
	renameFile = stub
	s.T().Cleanup(func() { renameFile = orig })
}

func (s *StoreSuite) open() *Store {
	st, err := OpenStore(s.dir, storePool, s.lock)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = st.Close() })
	st.now = func() time.Time { return s.clock }
	return st
}

func (s *StoreSuite) create() Session {
	sess, err := s.store.Create()
	s.Require().NoError(err)
	return sess
}

func (s *StoreSuite) snapshot() map[string]string {
	entries, err := os.ReadDir(s.dir)
	s.Require().NoError(err)
	out := map[string]string{}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		s.Require().NoError(err)
		out[e.Name()] = string(data)
	}
	return out
}

func (s *StoreSuite) writeFile(name, content string) string {
	root, err := os.OpenRoot(s.dir)
	s.Require().NoError(err)
	defer func() { _ = root.Close() }()
	s.Require().NoError(root.WriteFile(name, []byte(content), fileMode))
	return filepath.Join(s.dir, name)
}
