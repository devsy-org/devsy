//go:build !windows

package session

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const reservedID = "p-pool-aaaaaaaaaa"

func (s *StoreSuite) TestAdmitReplacesDeletedKeyAtRetentionBoundary() {
	lim := Limits{MaxConcurrent: 1}
	first := s.admit(canaryKey, lim)
	s.clock = s.clock.Add(2 * KeyRetention)
	s.Equal(first.Session, s.admit(canaryKey, lim).Session, "live keys do not expire")
	deleted := s.move(first.Session.ID, StateQueued, StateReclaiming, StateDeleted)
	s.clock = deleted.Updated.Add(KeyRetention - time.Nanosecond)
	before := s.poolSnapshot()
	s.Equal(Admission{Session: deleted, Existing: true}, s.admit(canaryKey, lim))
	s.Equal(before, s.poolSnapshot())

	s.clock = s.clock.Add(time.Nanosecond)
	next := s.admit(canaryKey, lim)
	s.False(next.Existing)
	s.NotEqual(deleted.ID, next.Session.ID)
	record, err := s.store.loadKey(keyDigest(canaryKey))
	s.Require().NoError(err)
	s.Equal(next.Session.ID, record.Session)
	s.Equal(s.clock, record.Created)
	s.Equal(Admission{Session: next.Session, Existing: true}, s.admit(canaryKey, lim))
	old, err := s.store.Load(deleted.ID)
	s.Require().NoError(err)
	s.Equal(deleted, old)
}

func (s *StoreSuite) TestAdmitDoesNotPublishSessionBeforeKey() {
	lim := Limits{MaxConcurrent: 1}
	s.writeReservation(canaryKey, s.clock.Add(-KeyRetention))
	before := s.poolSnapshot()
	s.stubRename(func(*os.Root, string, string) error { return errors.New("disk full") })
	got, err := s.store.Admit(canaryKey, lim)
	s.Require().ErrorContains(err, "disk full")
	s.Zero(got)
	s.NotContains(err.Error(), canaryKey)
	s.Equal(before, s.poolSnapshot())
}

func (s *StoreSuite) TestAdmitRepairsIncompleteReservationOnlyWhenCapacityAllows() {
	lim := Limits{MaxConcurrent: 1}
	useSource(s.T(), fixedReader{0xff})
	blocker := s.create()
	s.writeReservation(canaryKey, s.clock)
	s.clock = s.clock.Add(KeyRetention - time.Nanosecond)
	before := s.poolSnapshot()
	got, err := s.store.Admit(canaryKey, lim)
	s.Require().ErrorIs(err, ErrPoolFull)
	s.Zero(got)
	s.NotContains(err.Error(), canaryKey)
	s.Equal(before, s.poolSnapshot())
	s.move(blocker.ID, StateQueued, StateReclaiming, StateDeleted)

	repaired := s.admit(canaryKey, lim)
	s.True(repaired.Existing)
	s.Equal(reservedID, repaired.Session.ID)
	s.Equal(StateQueued, repaired.Session.State)
	s.Equal(s.clock, repaired.Session.Created)
	s.Equal(repaired, s.admit(canaryKey, lim))
	after := s.poolSnapshot()
	name := keysName + "/" + keyFile(canaryKey)
	s.Equal(before[name], after[name])
}

func (s *StoreSuite) TestAdmitReplacesExpiredIncompleteReservation() {
	s.writeReservation(canaryKey, s.clock)
	s.clock = s.clock.Add(KeyRetention)
	useSource(s.T(), bytes.NewReader(append(make([]byte, 7), bytes.Repeat([]byte{0xff}, 7)...)))
	got := s.admit(canaryKey, Limits{MaxConcurrent: 1})
	s.False(got.Existing)
	s.NotEqual(reservedID, got.Session.ID)
}

func (s *StoreSuite) TestAdmitChecksIDCollisionsBeforeReservingKey() {
	useSource(s.T(), fixedReader{0x00})
	s.create()
	before := s.poolSnapshot()
	got, err := s.store.Admit(canaryKey, Limits{MaxConcurrent: 2})
	s.Require().ErrorIs(err, ErrExists)
	s.Zero(got)
	s.Equal(before, s.poolSnapshot())
}

func (s *StoreSuite) TestPruneKeysRemovesOnlyExpiredRecords() {
	lim := Limits{MaxConcurrent: 3}
	deleted := s.admit("deleted", lim).Session
	s.move(deleted.ID, StateQueued, StateReclaiming, StateDeleted)
	s.admit("live", lim)
	s.writeReservation("old missing", s.clock)
	s.clock = s.clock.Add(time.Minute)
	s.writeReservation("recent missing", s.clock)
	recent := s.admit("recently deleted", lim).Session
	s.move(recent.ID, StateQueued, StateReclaiming, StateDeleted)
	s.clock = s.clock.Add(KeyRetention - time.Minute)

	before := s.snapshot()
	removed, err := s.store.PruneKeys()
	s.Require().NoError(err)
	s.Equal(2, removed)
	s.Equal(before, s.snapshot())
	s.ElementsMatch(
		[]string{keyFile("live"), keyFile("recent missing"), keyFile("recently deleted")},
		dirNames(s, s.layout.KeysDir()),
	)
	removed, err = s.store.PruneKeys()
	s.Require().NoError(err)
	s.Zero(removed)
}

func (s *StoreSuite) TestAdmitRefusesNoncanonicalKeyRecords() {
	hash := keyDigest(canaryKey)
	other := strings.Repeat("0", 64)
	canonical := s.keyJSON(hash, reservedID, s.clock)
	lim := Limits{MaxConcurrent: 1}
	for label, data := range map[string]string{
		"duplicate":  strings.Replace(canonical, `"v":1`, `"v":1,"v":1`, 1),
		"case":       strings.Replace(canonical, `"hash"`, `"Hash"`, 1),
		"wrong hash": s.keyJSON(other, reservedID, s.clock),
		"foreign ID": s.keyJSON(hash, "p-pool-other-aaaaaaaaaa", s.clock),
		"bad ID":     s.keyJSON(hash, "not-an-id", s.clock),
		"zero time":  s.keyJSON(hash, reservedID, time.Time{}),
		"trailing":   canonical + " ",
		"unknown":    strings.TrimSuffix(canonical, "}") + `,"note":"` + canaryKey + `"}`,
	} {
		s.Require().NoError(s.store.keys.WriteFile(keyFile(canaryKey), []byte(data), fileMode))
		got, err := s.store.Admit(canaryKey, lim)
		s.Require().ErrorIs(err, errInvalidKeyRecord, label)
		s.Zero(got)
		s.NotContains(err.Error(), canaryKey)
		removed, err := s.store.PruneKeys()
		s.Require().ErrorIs(err, errInvalidKeyRecord, label)
		s.Zero(removed)
	}
}

func (s *StoreSuite) TestAdmitRefusesUnsafeKeyFiles() {
	lim := Limits{MaxConcurrent: 1}
	name := keyFile(canaryKey)
	s.Require().
		NoError(s.store.keys.WriteFile(name, []byte(strings.Repeat(" ", maxFileBytes+1)), fileMode))
	_, err := s.store.Admit(canaryKey, lim)
	s.Require().ErrorIs(err, errTooLarge)
	s.Require().NoError(s.store.keys.Remove(name))

	s.writeReservation("target", s.clock)
	for _, target := range []string{keyFile("target"), "missing-target"} {
		s.Require().NoError(s.store.keys.Symlink(target, name))
		_, err = s.store.Admit(canaryKey, lim)
		s.Require().ErrorIs(err, errNotRegular)
		_, err = s.store.PruneKeys()
		s.Require().ErrorIs(err, errUnexpectedEntry)
		s.Require().NoError(s.store.keys.Remove(name))
	}
	s.Require().NoError(syscall.Mkfifo(filepath.Join(s.layout.KeysDir(), name), fileMode))
	_, err = s.store.Admit(canaryKey, lim)
	s.Require().ErrorIs(err, errNotRegular)
	_, err = s.store.PruneKeys()
	s.Require().ErrorIs(err, errUnexpectedEntry)
}

func (s *StoreSuite) TestMovedKeysDirectoryRefusesOperationsAndOpenRefusesSymlink() {
	other := s.newLayout(storePool)
	s.Require().NoError(os.MkdirAll(other.Dir(), dirMode))
	s.Require().NoError(os.Rename(s.layout.KeysDir(), other.KeysDir()))
	kept := filepath.Join(other.KeysDir(), ".tmp-1")
	s.Require().NoError(os.WriteFile(kept, nil, fileMode))
	_, err := s.store.List()
	s.Require().ErrorIs(err, errStoreMoved)
	_, err = s.store.Admit(canaryKey, Limits{MaxConcurrent: 1})
	s.Require().ErrorIs(err, errStoreMoved)
	_, _, err = s.store.Promote(Limits{MaxConcurrent: 1})
	s.Require().ErrorIs(err, errStoreMoved)
	_, err = s.store.PruneKeys()
	s.Require().ErrorIs(err, errStoreMoved)

	s.Require().NoError(os.Symlink(other.KeysDir(), s.layout.KeysDir()))
	_, err = OpenStore(s.dir, storePool, s.lock)
	s.Require().ErrorIs(err, errNotDirectory)
	s.Equal([]string{".tmp-1"}, dirNames(s, other.KeysDir()))
}

func (s *StoreSuite) TestOpenStoreRemovesOnlyRegularNumberedTempsFromKeys() {
	root := s.store.keys
	s.Require().NoError(root.WriteFile(".tmp-1", nil, fileMode))
	s.Require().NoError(root.WriteFile(".tmp-abc", nil, fileMode))
	s.Require().NoError(root.Symlink(".tmp-abc", ".tmp-2"))
	s.Require().NoError(root.Mkdir(".tmp-3", dirMode))
	s.open()
	s.Equal([]string{".tmp-2", ".tmp-3", ".tmp-abc"}, dirNames(s, s.layout.KeysDir()))
}

func keyDigest(key string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(key))) }

func keyFile(key string) string { return keyDigest(key) + fileExt }

func (s *StoreSuite) writeReservation(key string, created time.Time) {
	data := s.keyJSON(keyDigest(key), reservedID, created)
	s.Require().NoError(s.store.keys.WriteFile(keyFile(key), []byte(data), fileMode))
}

func (s *StoreSuite) keyJSON(hash, id string, created time.Time) string {
	stamp, err := created.MarshalJSON()
	s.Require().NoError(err)
	return fmt.Sprintf(`{"v":1,"hash":%q,"session":%q,"created":%s}`, hash, id, stamp)
}
