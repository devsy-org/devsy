//go:build !windows

package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type LayoutSuite struct{ suite.Suite }

func TestLayoutSuite(t *testing.T) { suite.Run(t, new(LayoutSuite)) }

func (s *LayoutSuite) TestNamesFollowThePoolDirectory() {
	l, err := NewLayout("/home/u/.devsy/pools", "team-a")
	s.Require().NoError(err)
	s.Equal("/home/u/.devsy/pools/team-a", l.Dir())
	s.Equal("/home/u/.devsy/pools/team-a/sessions", l.SessionsDir())
	s.Equal("/home/u/.devsy/pools/team-a/keys", l.KeysDir())
	s.Equal("/home/u/.devsy/pools/team-a/audit", l.AuditDir())
	s.Equal("/home/u/.devsy/pools/team-a/pool.sock", l.SocketPath())
}

func (s *LayoutSuite) TestPoolNameIsCheckedBeforeItBecomesADirectory() {
	for _, pool := range []string{"", "..", "a/b", "A", "a b"} {
		_, err := NewLayout("/tmp/pools", pool)
		s.Require().ErrorIs(err, errInvalidPool, pool)
	}
}

func (s *LayoutSuite) TestSecondOwnerIsRefusedUntilTheFirstReleases() {
	l := s.layout("pool")
	first, err := l.Lock()
	s.Require().NoError(err)
	info, err := os.Stat(filepath.Join(l.Dir(), lockName))
	s.Require().NoError(err)
	s.Equal(os.FileMode(fileMode), info.Mode().Perm())
	_, err = l.Lock()
	s.Require().ErrorIs(err, ErrPoolLocked)
	s.Require().NoError(first.Release())
	again, err := l.Lock()
	s.Require().NoError(err)
	s.Require().NoError(again.Release())
}

func (s *LayoutSuite) TestDifferentPoolsDoNotShareALock() {
	root := filepath.Join(s.T().TempDir(), "pools")
	a, err := NewLayout(root, "a")
	s.Require().NoError(err)
	b, err := NewLayout(root, "b")
	s.Require().NoError(err)
	la, err := a.Lock()
	s.Require().NoError(err)
	lb, err := b.Lock()
	s.Require().NoError(err)
	s.Require().NoError(la.Release())
	s.Require().NoError(lb.Release())
}

func (s *LayoutSuite) TestLockRefusesAGroupWritableDirectory() {
	l := s.layout("pool")
	s.Require().NoError(os.MkdirAll(l.Dir(), dirMode))
	s.Require().NoError(os.Chmod(l.Dir(), dirMode|0o050))
	_, err := l.Lock()
	s.Require().ErrorContains(err, "accessible only by its owner")
	s.NoFileExists(filepath.Join(l.Dir(), lockName))
}

func (s *LayoutSuite) layout(pool string) Layout {
	l, err := NewLayout(filepath.Join(s.T().TempDir(), "pools"), pool)
	s.Require().NoError(err)
	return l
}
