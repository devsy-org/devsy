package workspacelogs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Hop names one acknowledged position. A host that both receives and forwards
// keeps both, so durability at the next host is never inferred from durability
// in its own store.
type Hop string

// Acknowledgement hops.
const (
	// HopInbound is the position persisted in this host's store for a producer
	// that mirrors into it.
	HopInbound Hop = "inbound"
	// HopUpstream is the position the next host acknowledged for records this
	// host sends on.
	HopUpstream Hop = "upstream"
)

const ackMaxBytes = 4 * 1024

// AckKey identifies one stored position.
type AckKey struct {
	RunID      string
	ProducerID string
	Hop        Hop
}

// AckState is the durable position of one producer on one hop.
type AckState struct {
	SchemaVersion int    `json:"schemaVersion"`
	RunID         string `json:"runId"`
	ProducerID    string `json:"producerId"`
	Hop           Hop    `json:"hop"`
	// Acked is the highest sequence the peer persisted or represented as a gap.
	Acked uint64 `json:"acked"`
	// Gapped is set once the peer reported sequences it will never hold.
	Gapped   bool   `json:"gapped,omitempty"`
	GapFirst uint64 `json:"gapFirst,omitempty"`
	GapLast  uint64 `json:"gapLast,omitempty"`
}

// AckStore keeps acknowledged positions in small files beneath one private
// directory. Positions only move forward.
type AckStore struct {
	dir string
	mu  sync.Mutex
}

// NewAckStore creates dir (absolute) and returns the store.
func NewAckStore(dir string) (*AckStore, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("%w: ack directory must be absolute", ErrUnsafePath)
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("create ack directory: %w", err)
	}

	return &AckStore{dir: dir}, nil
}

// Load returns the stored position, or a zero position when none exists.
func (a *AckStore) Load(key AckKey) (AckState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.load(key)
}

// Advance moves the acknowledged position forward. A lower value is ignored.
func (a *AckStore) Advance(key AckKey, acked uint64) error {
	return a.update(key, func(st *AckState) {
		st.Acked = max(st.Acked, acked)
	})
}

// RecordGap stores that the peer will never hold first..last and moves the
// position past the gap so the sender stops retrying those bytes.
func (a *AckStore) RecordGap(key AckKey, first, last uint64) error {
	return a.update(key, func(st *AckState) {
		if !st.Gapped {
			st.GapFirst = first
		}
		st.Gapped = true
		st.GapLast = max(st.GapLast, last)
		st.Acked = max(st.Acked, last)
	})
}

func (a *AckStore) update(key AckKey, fn func(*AckState)) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, err := a.load(key)
	if err != nil {
		return err
	}
	fn(&st)

	return a.write(st)
}

func (a *AckStore) path(key AckKey) (string, error) {
	if err := requireID("runId", key.RunID); err != nil {
		return "", err
	}
	if err := requireID("producerId", key.ProducerID); err != nil {
		return "", err
	}
	if key.Hop != HopInbound && key.Hop != HopUpstream {
		return "", fmt.Errorf("%w: hop", ErrInvalidRecord)
	}

	return filepath.Join(a.dir, key.RunID+"."+key.ProducerID+"."+string(key.Hop)+".json"), nil
}

func (a *AckStore) load(key AckKey) (AckState, error) {
	zero := AckState{
		SchemaVersion: SchemaVersion, RunID: key.RunID, ProducerID: key.ProducerID, Hop: key.Hop,
	}
	path, err := a.path(key)
	if err != nil {
		return zero, err
	}
	data, err := readSmallFile(path)
	if err != nil || data == nil {
		return zero, err
	}
	var st AckState
	if json.Unmarshal(data, &st) != nil || st.RunID != key.RunID ||
		st.ProducerID != key.ProducerID || st.Hop != key.Hop {
		// A damaged position restarts from zero; the receiver deduplicates.
		return zero, nil
	}

	return st, nil
}

// readSmallFile returns nil data for a missing or oversized file.
func readSmallFile(path string) ([]byte, error) {
	if err := checkRegular(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is built from validated IDs
	if errors.Is(err, fs.ErrNotExist) || len(data) > ackMaxBytes {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read ack: %w", err)
	}

	return data, nil
}

func (a *AckStore) write(st AckState) error {
	path, err := a.path(AckKey{RunID: st.RunID, ProducerID: st.ProducerID, Hop: st.Hop})
	if err != nil {
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("encode ack: %w", err)
	}
	tmp, err := os.CreateTemp(a.dir, "ack-*.tmp")
	if err != nil {
		return fmt.Errorf("create ack temp file: %w", err)
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err = errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("write ack: %w", err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("replace ack: %w", err)
	}

	return nil
}
