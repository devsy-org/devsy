//go:build !windows

package session

import (
	"errors"
	"os"
)

var errInjectedIO = errors.New("injected I/O failure")

func (s *StoreSuite) TestAdmitRetriesSyncAfterFreshKeyLink() {
	var written bool
	orig := linkFile
	linkFile = func(root *os.Root, from, to string) error {
		written = written || root == s.store.keys
		return orig(root, from, to)
	}
	s.T().Cleanup(func() { linkFile = orig })
	s.checkKeySyncRetries(&written)
}

func (s *StoreSuite) TestAdmitRetriesSyncAfterExpiredKeyRename() {
	s.writeReservation(canaryKey, s.clock.Add(-KeyRetention))
	renames := 0
	var written bool
	s.stubRename(func(root *os.Root, from, to string) error {
		s.Same(s.store.keys, root)
		s.Equal(keyFile(canaryKey), to)
		renames++
		written = true
		return root.Rename(from, to)
	})
	s.checkKeySyncRetries(&written)
	s.Equal(1, renames)
}

// checkKeySyncRetries fails every key directory sync once the key record is
// written, so the record is visible but not durable.
func (s *StoreSuite) checkKeySyncRetries(written *bool) {
	useSource(s.T(), fixedReader{0xff})
	fail := true
	var synced []*os.Root
	s.stubSync(func(root *os.Root) error {
		synced = append(synced, root)
		if root == s.store.keys {
			s.Empty(s.names(), "the key directory must sync before the session is published")
			if fail && *written {
				return errInjectedIO
			}
		}
		return syncDirectory(root)
	})

	for _, want := range [][]*os.Root{{s.store.keys, s.store.keys}, {s.store.keys}} {
		got, err := s.store.Admit(canaryKey)
		s.Require().ErrorIs(err, errInjectedIO)
		s.Zero(got)
		s.Equal(want, synced)
		s.Empty(s.names())
		synced = nil
	}
	record, err := s.store.loadKey(keyDigest(canaryKey))
	s.Require().NoError(err)
	s.NotEqual(reservedID, record.Session)
	s.Equal(s.clock, record.Created)
	fail = false
	got := s.admit(canaryKey)
	s.True(got.Existing)
	s.Equal(record.Session, got.Session.ID)
	s.Equal([]*os.Root{s.store.keys, s.store.root}, synced)
	s.Equal([]string{fileName(record.Session)}, s.names())
}

func (s *StoreSuite) TestAdmitRetriesSyncBeforeAcknowledgingExistingSession() {
	fail := true
	var synced []*os.Root
	s.stubSync(func(root *os.Root) error {
		synced = append(synced, root)
		if fail && root == s.store.root {
			return errInjectedIO
		}
		return syncDirectory(root)
	})
	keys, sessions := s.store.keys, s.store.root
	for _, want := range [][]*os.Root{{keys, keys, sessions}, {keys, sessions}} {
		got, err := s.store.Admit(canaryKey)
		s.Require().ErrorIs(err, errInjectedIO)
		s.Zero(got)
		s.Equal(want, synced)
		s.Require().Len(s.names(), 1)
		synced = nil
	}

	fail = false
	got := s.admit(canaryKey)
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
		store, err := OpenStore(s.dir, storePool, s.lock, Limits{MaxConcurrent: 1})
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
	s.store = s.openWith(Limits{MaxConcurrent: 2})
	fail := true
	orig := linkFile
	linkFile = func(root *os.Root, from, to string) error {
		if fail && root == s.store.keys {
			return errInjectedIO
		}
		return orig(root, from, to)
	}
	s.T().Cleanup(func() { linkFile = orig })

	got, err := s.store.Admit(canaryKey)
	s.Require().ErrorIs(err, errInjectedIO)
	s.Zero(got)
	s.Empty(s.names())
	s.Empty(dirNames(s, s.layout.KeysDir()))

	fail = false
	got = s.admit(canaryKey)
	s.False(got.Existing)
	s.Equal([]string{fileName(got.Session.ID)}, s.names())
	s.Equal([]string{keyFile(canaryKey)}, dirNames(s, s.layout.KeysDir()))
}

func (s *StoreSuite) TestRepairAssignsNextSequenceAfterCreateAndFreshAdmit() {
	lim := Limits{MaxConcurrent: 3}
	s.store = s.openWith(lim)
	interruptedRoot := s.store.root
	orig := linkFile
	linkFile = func(root *os.Root, from, to string) error {
		if root == interruptedRoot {
			return errInjectedIO
		}
		return orig(root, from, to)
	}
	s.T().Cleanup(func() { linkFile = orig })

	useSource(s.T(), fixedReader{0x00})
	got, err := s.store.Admit(canaryKey)
	s.Require().ErrorIs(err, errInjectedIO)
	s.Zero(got)
	s.Empty(s.names())
	s.Require().NoError(s.store.Close())
	s.store = s.openWith(lim)
	record, err := s.store.loadKey(keyDigest(canaryKey))
	s.Require().NoError(err)

	useSource(s.T(), fixedReader{0x40})
	created := s.create()
	useSource(s.T(), fixedReader{0xff})
	fresh := s.admit("fresh key")
	repaired := s.admit(canaryKey)
	s.False(fresh.Existing)
	s.True(repaired.Existing)
	s.Equal(record.Session, repaired.Session.ID)
	s.Equal([]uint64{1, 2, 3}, []uint64{created.Seq, fresh.Session.Seq, repaired.Session.Seq})
}

func (s *StoreSuite) TestCreateSyncsKeysBeforeChoosingAnID() {
	s.writeReservation("old", s.clock)
	s.clock = s.clock.Add(KeyRetention)
	fail := true
	var synced []*os.Root
	s.stubSync(func(root *os.Root) error {
		synced = append(synced, root)
		if fail && root == s.store.keys {
			return errInjectedIO
		}
		return syncDirectory(root)
	})
	removed, err := s.store.PruneKeys()
	s.Require().ErrorIs(err, errInjectedIO)
	s.Equal(1, removed)
	s.Empty(dirNames(s, s.layout.KeysDir()))

	useSource(s.T(), fixedReader{0})
	synced = nil
	got, err := s.store.create()
	s.Require().ErrorIs(err, errInjectedIO)
	s.Zero(got)
	s.Equal([]*os.Root{s.store.keys}, synced)
	s.Empty(s.names())

	fail = false
	synced = nil
	_, err = s.store.create()
	s.Require().NoError(err)
	s.Equal(s.store.keys, synced[0])
}

func (s *StoreSuite) TestCreateSyncsKeysAfterAnExpiredReservationIsReplaced() {
	s.store = s.openWith(Limits{MaxConcurrent: 2})
	s.writeReservation(canaryKey, s.clock.Add(-KeyRetention))
	fail := true
	s.stubSync(func(root *os.Root) error {
		if fail && root == s.store.keys {
			return errInjectedIO
		}
		return syncDirectory(root)
	})
	useSource(s.T(), fixedReader{0xff})
	got, err := s.store.Admit(canaryKey)
	s.Require().ErrorIs(err, errInjectedIO)
	s.Zero(got)

	useSource(s.T(), fixedReader{0})
	created, err := s.store.create()
	s.Require().ErrorIs(err, errInjectedIO)
	s.Zero(created)
	s.Empty(s.names())
}

func (s *StoreSuite) stubSync(stub func(*os.Root) error) {
	orig := syncRoot
	syncRoot = stub
	s.T().Cleanup(func() { syncRoot = orig })
}
