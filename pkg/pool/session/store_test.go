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
	dir   string
	store *Store
	clock time.Time
}

func TestStoreSuite(t *testing.T) { suite.Run(t, new(StoreSuite)) }

func (s *StoreSuite) SetupTest() {
	s.dir = filepath.Join(s.T().TempDir(), "sessions")
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
	_, err := s.store.Create()
	s.Require().ErrorIs(err, ErrExists)
	s.Equal(before, s.snapshot())
	s.Equal("p-pool-aaaaaaaaaa", first.ID)
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
	renameFile = func(string, string) error { return errors.New("disk full") }
	s.T().Cleanup(func() { renameFile = os.Rename })
	_, err := s.store.Transition(sess.ID, StateQueued, StateSpawning, "")
	s.Require().ErrorContains(err, "disk full")
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
	s.Require().ErrorIs(err, syscall.ELOOP)

	fifo := "p-pool-eeeeeeeeee"
	s.Require().NoError(syscall.Mkfifo(filepath.Join(s.dir, fifo+fileExt), fileMode))
	_, err = s.store.Load(fifo)
	s.Require().ErrorIs(err, errNotRegular)

	s.writeFile("p-pool-ffffffffff.json", string(valid[:len(valid)-1])+`,"extra":1}`)
	_, err = s.store.Load("p-pool-ffffffffff")
	s.Require().ErrorIs(err, errInvalidSession)
}

func (s *StoreSuite) TestOpenStoreChecksTheDirectory() {
	_, err := OpenStore(s.dir, "Bad Pool")
	s.Require().ErrorIs(err, errInvalidPool)

	loose := filepath.Join(s.T().TempDir(), "loose")
	s.Require().NoError(os.Mkdir(loose, dirMode))
	s.Require().NoError(os.Chmod(loose, dirMode|0o070))
	_, err = OpenStore(loose, storePool)
	s.Require().ErrorContains(err, "must not be writable by group or others")

	readable := filepath.Join(s.T().TempDir(), "readable")
	s.Require().NoError(os.Mkdir(readable, 0o750))
	_, err = OpenStore(readable, storePool)
	s.Require().NoError(err)

	nested := filepath.Join(s.T().TempDir(), "a", "b", "sessions")
	_, err = OpenStore(nested, storePool)
	s.Require().NoError(err)
	info, err := os.Stat(filepath.Dir(nested))
	s.Require().NoError(err)
	s.Equal(os.FileMode(dirMode), info.Mode().Perm())
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

func (s *StoreSuite) open() *Store {
	st, err := OpenStore(s.dir, storePool)
	s.Require().NoError(err)
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
