//go:build !windows

package session

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

const (
	helperRootEnv  = "SESSION_TEST_HELPER_ROOT"
	helperModeEnv  = "SESSION_TEST_HELPER_MODE"
	helperPool     = "pool"
	helperProcs    = 6
	helperSessions = 30
	contendTries   = 300
	perRound       = 4
)

// TestHelperProcess is the body of the child processes ProcessSuite starts.
func TestHelperProcess(t *testing.T) {
	root := os.Getenv(helperRootEnv)
	if root == "" {
		t.Skip("runs only as a child of ProcessSuite")
	}
	os.Exit(runHelper(root, os.Getenv(helperModeEnv)))
}

func runHelper(root, mode string) int {
	layout, err := NewLayout(root, helperPool)
	if err != nil {
		return failHelper(err)
	}
	switch mode {
	case "contend":
		return contend(layout)
	case "race":
		return race(layout)
	case "admit":
		return raceAdmit(layout)
	}
	return failHelper(fmt.Errorf("unknown mode %q", mode))
}

func say(args ...any) { _, _ = fmt.Fprintln(os.Stdout, args...) }

func failHelper(err error) int {
	say("error:", err)
	return 1
}

// contend must be refused the lock, and a store without it, every time.
func contend(layout Layout) int {
	for range contendTries {
		lock, err := layout.Lock()
		if lock != nil || !errors.Is(err, ErrPoolLocked) {
			return failHelper(fmt.Errorf("lock was not refused: %v", err))
		}
		if _, err := OpenStore(layout.SessionsDir(), helperPool, nil); err == nil {
			return failHelper(errors.New("store opened without a lock"))
		}
	}
	say("refused", contendTries)
	return 0
}

// race takes the lock in turns with its siblings and moves a few queued
// sessions each time. With the lock held no transition may ever conflict.
func race(layout Layout) int {
	wins := 0
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		n, queued, err := raceRound(layout)
		if err != nil {
			return failHelper(err)
		}
		wins += n
		if !queued {
			say("wins", wins)
			return 0
		}
	}
	return failHelper(errors.New("timed out"))
}

// raceRound reports how many sessions it moved and whether any were queued.
func raceRound(layout Layout) (int, bool, error) {
	lock, err := layout.Lock()
	if errors.Is(err, ErrPoolLocked) {
		time.Sleep(time.Millisecond)
		return 0, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = lock.Release() }()
	store, err := OpenStore(layout.SessionsDir(), helperPool, lock)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = store.Close() }()
	return moveQueued(store)
}

func raceAdmit(layout Layout) int {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		lock, err := layout.Lock()
		if errors.Is(err, ErrPoolLocked) {
			time.Sleep(time.Millisecond)
			continue
		}
		if err != nil {
			return failHelper(err)
		}
		defer func() { _ = lock.Release() }()
		return admitUnderLock(layout, lock)
	}
	return failHelper(errors.New("timed out"))
}

func admitUnderLock(layout Layout, lock *Lock) int {
	store, err := OpenStore(layout.SessionsDir(), helperPool, lock)
	if err != nil {
		return failHelper(err)
	}
	defer func() { _ = store.Close() }()
	got, err := store.Admit("shared key", Limits{MaxConcurrent: 1})
	if err != nil {
		return failHelper(err)
	}
	say(got.Session.ID, got.Existing)
	return 0
}

// moveQueued moves up to perRound queued sessions and reports whether more
// are left.
func moveQueued(store *Store) (int, bool, error) {
	for wins := range perRound {
		_, ok, err := store.Promote(Limits{MaxConcurrent: 2 * helperSessions})
		if err != nil || !ok {
			return wins, false, err
		}
	}
	return perRound, true, nil
}

type ProcessSuite struct {
	suite.Suite
	root   string
	layout Layout
}

func TestProcessSuite(t *testing.T) { suite.Run(t, new(ProcessSuite)) }

func (s *ProcessSuite) SetupTest() {
	s.root = filepath.Join(s.T().TempDir(), "pools")
	layout, err := NewLayout(s.root, helperPool)
	s.Require().NoError(err)
	s.layout = layout
}

type helpers struct {
	cmds []*exec.Cmd
	outs []*bytes.Buffer
}

func (s *ProcessSuite) TestAdmitSharesOneSessionAcrossLockHolders() {
	lock, err := s.layout.Lock()
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = lock.Release() })
	children := s.start("admit", helperProcs)
	s.Require().NoError(lock.Release())
	results := s.wait(children)

	again, err := s.layout.Lock()
	s.Require().NoError(err)
	defer func() { _ = again.Release() }()
	store, err := OpenStore(s.layout.SessionsDir(), helperPool, again)
	s.Require().NoError(err)
	defer func() { _ = store.Close() }()
	list, err := store.List()
	s.Require().NoError(err)
	s.Require().Len(list, 1)
	want := []string{list[0].ID + " false"}
	for range helperProcs - 1 {
		want = append(want, list[0].ID+" true")
	}
	s.ElementsMatch(want, results)
}

func (s *ProcessSuite) TestOnlyTheLockHolderCanWriteSessions() {
	lock, err := s.layout.Lock()
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = lock.Release() })
	store, err := OpenStore(s.layout.SessionsDir(), helperPool, lock)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = store.Close() })
	for range helperSessions {
		_, err := store.create()
		s.Require().NoError(err)
	}

	contenders := s.start("contend", helperProcs)
	for range helperSessions {
		_, ok, err := store.Promote(Limits{MaxConcurrent: helperSessions})
		s.Require().NoError(err, "no other process may touch a session the holder owns")
		s.Require().True(ok)
	}
	for _, line := range s.wait(contenders) {
		s.Equal("refused "+strconv.Itoa(contendTries), line)
	}
	list, err := store.List()
	s.Require().NoError(err)
	s.Len(list, helperSessions)
	for _, sess := range list {
		s.Equal(StateSpawning, sess.State)
	}

	for range helperSessions {
		_, err := store.create()
		s.Require().NoError(err)
	}
	s.Require().NoError(store.Close())
	s.Require().NoError(lock.Release())
	s.assertEveryQueuedSessionMovedOnce()
}

func (s *ProcessSuite) assertEveryQueuedSessionMovedOnce() {
	total := 0
	for _, line := range s.wait(s.start("race", helperProcs)) {
		n, err := strconv.Atoi(strings.TrimPrefix(line, "wins "))
		s.Require().NoError(err, line)
		total += n
	}
	s.Equal(helperSessions, total, "each queued session is won by exactly one process")

	lock, err := s.layout.Lock()
	s.Require().NoError(err)
	defer func() { _ = lock.Release() }()
	store, err := OpenStore(s.layout.SessionsDir(), helperPool, lock)
	s.Require().NoError(err)
	defer func() { _ = store.Close() }()
	list, err := store.List()
	s.Require().NoError(err)
	s.Len(list, 2*helperSessions)
	for _, sess := range list {
		s.Equal(StateSpawning, sess.State)
	}
}

func (s *ProcessSuite) start(mode string, n int) *helpers {
	exe, err := os.Executable()
	s.Require().NoError(err)
	h := &helpers{}
	for range n {
		out := new(bytes.Buffer)
		cmd := &exec.Cmd{
			Path:   exe,
			Args:   []string{exe, "-test.run=^TestHelperProcess$", "-test.count=1"},
			Env:    append(os.Environ(), helperRootEnv+"="+s.root, helperModeEnv+"="+mode),
			Stdout: out,
			Stderr: out,
		}
		s.Require().NoError(cmd.Start())
		s.T().Cleanup(func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		h.cmds, h.outs = append(h.cmds, cmd), append(h.outs, out)
	}
	return h
}

// wait returns the last output line of every helper and fails the test for any
// helper that exited with an error.
func (s *ProcessSuite) wait(h *helpers) []string {
	lines := make([]string, len(h.cmds))
	var wg sync.WaitGroup
	for i, cmd := range h.cmds {
		wg.Go(func() {
			timer := time.AfterFunc(40*time.Second, func() { _ = cmd.Process.Kill() })
			defer timer.Stop()
			err := cmd.Wait()
			out := strings.TrimSpace(h.outs[i].String())
			lines[i] = out[strings.LastIndex(out, "\n")+1:]
			s.NoError(err, out)
		})
	}
	wg.Wait()
	return lines
}
