//go:build !windows

package session

import (
	"errors"
	"os"
)

var errInjectedIO = errors.New("injected I/O failure")

func (s *StoreSuite) TestAdmitRetriesSyncAfterFreshKeyLink() {
	s.checkKeySyncRetries()
}

func (s *StoreSuite) TestAdmitRetriesSyncAfterExpiredKeyRename() {
	s.writeReservation(canaryKey, s.clock.Add(-KeyRetention))
	renames := 0
	s.stubRename(func(root *os.Root, from, to string) error {
		s.Same(s.store.keys, root)
		s.Equal(keyFile(canaryKey), to)
		renames++
		return root.Rename(from, to)
	})
	s.checkKeySyncRetries()
	s.Equal(1, renames)
}

func (s *StoreSuite) checkKeySyncRetries() {
	useSource(s.T(), fixedReader{0xff})
	lim := Limits{MaxConcurrent: 1}
	fail := true
	var synced []*os.Root
	s.stubSync(func(root *os.Root) error {
		synced = append(synced, root)
		if root == s.store.keys {
			s.Empty(s.names(), "the key directory must sync before the session is published")
			if fail {
				return errInjectedIO
			}
		}
		return syncDirectory(root)
	})

	for range 2 {
		got, err := s.store.Admit(canaryKey, lim)
		s.Require().ErrorIs(err, errInjectedIO)
		s.Zero(got)
		s.Equal([]*os.Root{s.store.keys}, synced)
		s.Empty(s.names())
		synced = nil
	}
	record, err := s.store.loadKey(keyDigest(canaryKey))
	s.Require().NoError(err)
	s.NotEqual(reservedID, record.Session)
	s.Equal(s.clock, record.Created)
	fail = false
	got := s.admit(canaryKey, lim)
	s.True(got.Existing)
	s.Equal(record.Session, got.Session.ID)
	s.Equal([]*os.Root{s.store.keys, s.store.root}, synced)
	s.Equal([]string{fileName(record.Session)}, s.names())
}

func (s *StoreSuite) TestAdmitRetriesSyncBeforeAcknowledgingExistingSession() {
	lim := Limits{MaxConcurrent: 1}
	fail := true
	var synced []*os.Root
	s.stubSync(func(root *os.Root) error {
		synced = append(synced, root)
		if fail && root == s.store.root {
			return errInjectedIO
		}
		return syncDirectory(root)
	})
	for range 2 {
		got, err := s.store.Admit(canaryKey, lim)
		s.Require().ErrorIs(err, errInjectedIO)
		s.Zero(got)
		s.Equal([]*os.Root{s.store.keys, s.store.root}, synced)
		s.Require().Len(s.names(), 1)
		synced = nil
	}

	fail = false
	got := s.admit(canaryKey, lim)
	s.True(got.Existing)
	s.Equal([]*os.Root{s.store.keys, s.store.root}, synced)
	s.Equal([]string{fileName(got.Session.ID)}, s.names())
}

func (s *StoreSuite) TestOpenStoreRetriesPoolDirectorySync() {
	s.Require().NoError(os.Remove(s.layout.KeysDir()))
	fail := true
	var synced []*os.Root
	s.stubSync(func(root *os.Root) error {
		synced = append(synced, root)
		if fail {
			return errInjectedIO
		}
		return syncDirectory(root)
	})
	for range 2 {
		store, err := OpenStore(s.dir, storePool, s.lock)
		s.Require().ErrorIs(err, errInjectedIO)
		s.Nil(store)
		s.DirExists(s.layout.KeysDir())
		s.Equal([]*os.Root{s.lock.root}, synced)
		synced = nil
	}
	fail = false
	s.open()
	s.Equal([]*os.Root{s.lock.root}, synced)
}

func (s *StoreSuite) TestAdmitDoesNotPublishSessionWhenFreshKeyLinkFails() {
	lim := Limits{MaxConcurrent: 2}
	fail := true
	orig := linkFile
	linkFile = func(root *os.Root, from, to string) error {
		if fail && root == s.store.keys {
			return errInjectedIO
		}
		return orig(root, from, to)
	}
	s.T().Cleanup(func() { linkFile = orig })

	got, err := s.store.Admit(canaryKey, lim)
	s.Require().ErrorIs(err, errInjectedIO)
	s.Zero(got)
	s.Empty(s.names())
	s.Empty(dirNames(s, s.layout.KeysDir()))

	fail = false
	got = s.admit(canaryKey, lim)
	s.False(got.Existing)
	s.Equal([]string{fileName(got.Session.ID)}, s.names())
	s.Equal([]string{keyFile(canaryKey)}, dirNames(s, s.layout.KeysDir()))
}

func (s *StoreSuite) stubSync(stub func(*os.Root) error) {
	orig := syncRoot
	syncRoot = stub
	s.T().Cleanup(func() { syncRoot = orig })
}
