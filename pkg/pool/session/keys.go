package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// KeyRetention is how long a key remains usable after deletion or an
// incomplete reservation.
const KeyRetention = 24 * time.Hour

// ErrInvalidKey means the supplied idempotency key is not valid.
var ErrInvalidKey = errors.New("invalid idempotency key")

var (
	errInvalidKeyRecord = errors.New("invalid idempotency key record")
	hashPattern         = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type keyRecord struct {
	V       int       `json:"v"`
	Hash    string    `json:"hash"`
	Session string    `json:"session"`
	Created time.Time `json:"created"`
}

func hashKey(key string) (string, error) {
	if key == "" || len(key) > 256 || !utf8.ValidString(key) ||
		strings.ContainsFunc(key, unicode.IsControl) {
		return "", ErrInvalidKey
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:]), nil
}

func (s *Store) loadKey(hash string) (keyRecord, error) {
	data, err := readRegular(s.keys, fileName(hash))
	if err != nil {
		return keyRecord{}, err
	}
	return decodeKey(data, hash, s.pool)
}

func decodeKey(data []byte, hash, pool string) (keyRecord, error) {
	var record keyRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return keyRecord{}, errInvalidKeyRecord
	}
	if !record.valid(hash, pool) {
		return keyRecord{}, errInvalidKeyRecord
	}
	canonical, err := json.Marshal(record)
	if err != nil || !bytes.Equal(canonical, data) {
		return keyRecord{}, errInvalidKeyRecord
	}
	return record, nil
}

func (r keyRecord) valid(hash, pool string) bool {
	if r.V != schemaVersion || r.Hash != hash || r.Created.IsZero() ||
		!hashPattern.MatchString(r.Hash) {
		return false
	}
	prefix := idPrefix + pool + "-"
	return len(r.Session) == len(prefix)+idSuffixLen &&
		strings.HasPrefix(r.Session, prefix) && ValidID(r.Session)
}

func (s *Store) writeKey(record keyRecord, overwrite bool) error {
	if !record.valid(record.Hash, s.pool) {
		return errInvalidKeyRecord
	}
	data, err := json.Marshal(record)
	if err != nil {
		return errInvalidKeyRecord
	}
	if overwrite {
		return replace(s.keys, fileName(record.Hash), data)
	}
	return publish(s.keys, fileName(record.Hash), data)
}

func (s *Store) keySession(record keyRecord, now time.Time) (Session, bool, error) {
	sess, err := s.read(record.Session)
	switch {
	case errors.Is(err, ErrNotFound):
		return Session{}, now.Sub(record.Created) >= KeyRetention, nil
	case err != nil:
		return Session{}, false, err
	}
	return sess, sess.State == StateDeleted && now.Sub(sess.Updated) >= KeyRetention, nil
}

// PruneKeys removes expired key records. A session file must outlive its key
// record, so future session pruning must remove the key first.
func (s *Store) PruneKeys() (int, error) {
	unlock, err := s.hold()
	if err != nil {
		return 0, err
	}
	defer unlock()
	names, err := s.expiredKeys(s.now())
	if err != nil || len(names) == 0 {
		return 0, err
	}
	for i, name := range names {
		if err := s.keys.Remove(name); err != nil {
			return i, errors.Join(fmt.Errorf("remove key record: %w", err), syncRoot(s.keys))
		}
	}
	return len(names), syncRoot(s.keys)
}

func (s *Store) expiredKeys(now time.Time) ([]string, error) {
	records, err := s.keyRecords()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, record := range records {
		_, expired, err := s.keySession(record, now)
		if err != nil {
			return nil, err
		}
		if expired {
			names = append(names, fileName(record.Hash))
		}
	}
	return names, nil
}

func (s *Store) keyRecords() ([]keyRecord, error) {
	list, err := entries(s.keys)
	if err != nil {
		return nil, err
	}
	records := make([]keyRecord, 0, len(list))
	for _, entry := range list {
		if isTemp(entry) {
			continue
		}
		if !entry.Type().IsRegular() {
			return nil, errUnexpectedEntry
		}
		record, err := s.loadNamedKey(entry.Name())
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func (s *Store) loadNamedKey(name string) (keyRecord, error) {
	hash, ok := strings.CutSuffix(name, fileExt)
	if !ok || !hashPattern.MatchString(hash) {
		return keyRecord{}, errUnexpectedEntry
	}
	return s.loadKey(hash)
}
