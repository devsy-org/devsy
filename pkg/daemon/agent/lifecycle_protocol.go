package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	// LifecycleProtocolVersion is bumped when owner or control records change shape.
	LifecycleProtocolVersion = 1

	ownerFileName    = "owner.json"
	controlFileName  = "control.json"
	maxRecordBytes   = 4096
	connectPollEvery = 100 * time.Millisecond
	runtimeDirMode   = 0o755
	recordFileMode   = 0o644
)

var (
	// ErrLifecycleOwnerMissing means no live managed daemon published an owner record.
	ErrLifecycleOwnerMissing = errors.New("container lifecycle owner not found")
	// ErrLifecycleOwnerIncompatible means the owner speaks another protocol version.
	ErrLifecycleOwnerIncompatible = errors.New("container lifecycle owner is incompatible")
)

// LifecycleState is what setup tells the daemon about the timer.
type LifecycleState string

const (
	// LifecycleStateSetup suspends the timer while synchronous setup runs.
	LifecycleStateSetup LifecycleState = "setup"
	// LifecycleStateReady resets activity and arms the timer.
	LifecycleStateReady LifecycleState = "ready"
	// LifecycleStateFailed releases the suspension and starts a fresh idle interval.
	LifecycleStateFailed LifecycleState = "failed"
)

// OwnerRecord is published by the daemon process that owns the timer.
type OwnerRecord struct {
	Version    int    `json:"version"`
	Generation string `json:"generation"`
	PID        int    `json:"pid"`
	StartTicks string `json:"startTicks,omitempty"`
}

// ControlRecord is written by setup. It never carries credentials.
type ControlRecord struct {
	Version        int            `json:"version"`
	Generation     string         `json:"generation"`
	Timeout        string         `json:"timeout"`
	ShutdownAction string         `json:"shutdownAction"`
	State          LifecycleState `json:"state"`
}

// trustedOwner reports whether files owned by uid may be believed by a process
// running as euid.
func trustedOwner(uid, euid int) bool {
	return uid == euid || uid == 0
}

func checkDir(dir string, euid int) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("lifecycle runtime path %s is not a directory", dir)
	}
	if uid, ok := fileOwner(fi); ok && !trustedOwner(uid, euid) {
		return fmt.Errorf("lifecycle runtime dir %s is owned by uid %d", dir, uid)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("lifecycle runtime dir %s is writable by others", dir)
	}
	return nil
}

func writeRecord(dir, name string, v any, euid int) error {
	if err := checkDir(dir, euid); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+name+"."+hex.EncodeToString(suffix)+".tmp")
	f, err := openNoFollow(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, recordFileMode)
	if err != nil {
		return fmt.Errorf("create lifecycle record: %w", err)
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write lifecycle record: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish lifecycle record: %w", err)
	}
	return nil
}

// recordReader loads records from a runtime dir. When strict, the dir and each
// file must be owned by a trusted uid; lenient reads only reject symlinks and
// non-regular files.
type recordReader struct {
	dir    string
	euid   int
	strict bool
}

func (r recordReader) read(name string, out any) error {
	if r.strict {
		if err := checkDir(r.dir, r.euid); err != nil {
			return err
		}
	}
	f, err := openNoFollow(filepath.Join(r.dir, name), os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := r.checkFile(f, name); err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxRecordBytes))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (r recordReader) checkFile(f *os.File, name string) error {
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxRecordBytes {
		return fmt.Errorf("lifecycle record %s is not a small regular file", name)
	}
	if uid, ok := fileOwner(fi); r.strict && ok && !trustedOwner(uid, r.euid) {
		return fmt.Errorf("lifecycle record %s is owned by uid %d", name, uid)
	}
	return nil
}

// LifecycleOwner is the daemon-side handle of the lifecycle protocol.
type LifecycleOwner struct {
	dir        string
	euid       int
	generation string
	last       ControlRecord
}

// NewLifecycleOwner prepares a private runtime dir, discards records from any
// earlier process, and publishes a fresh owner record.
func NewLifecycleOwner(dir string) (*LifecycleOwner, error) {
	euid := os.Geteuid()
	if err := prepareRuntimeDir(dir, euid); err != nil {
		return nil, err
	}
	gen := make([]byte, 16)
	if _, err := rand.Read(gen); err != nil {
		return nil, err
	}
	o := &LifecycleOwner{dir: dir, euid: euid, generation: hex.EncodeToString(gen)}
	pid := os.Getpid()
	err := writeRecord(dir, ownerFileName, OwnerRecord{
		Version:    LifecycleProtocolVersion,
		Generation: o.generation,
		PID:        pid,
		StartTicks: procStartTicks(pid),
	}, euid)
	if err != nil {
		return nil, err
	}
	return o, nil
}

func prepareRuntimeDir(dir string, euid int) error {
	if err := os.MkdirAll(filepath.Dir(dir), runtimeDirMode); err != nil {
		return err
	}
	if err := os.Mkdir(dir, runtimeDirMode); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := requireOwnedDir(dir, euid); err != nil {
		return err
	}
	if err := os.Chmod(dir, runtimeDirMode); err != nil {
		return err
	}
	if err := checkDir(dir, euid); err != nil {
		return err
	}
	return discardStaleRecords(dir)
}

func requireOwnedDir(dir string, euid int) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if uid, ok := fileOwner(fi); ok && uid != euid {
		return fmt.Errorf("lifecycle runtime dir %s is owned by uid %d, not %d", dir, uid, euid)
	}
	return nil
}

// discardStaleRecords removes records from an earlier process or mount so they
// cannot arm this one.
func discardStaleRecords(dir string) error {
	for _, name := range []string{ownerFileName, controlFileName} {
		if err := os.Remove(
			filepath.Join(dir, name),
		); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Generation identifies this daemon process.
func (o *LifecycleOwner) Generation() string { return o.generation }

// PollControl returns the control record when it is valid for this generation
// and differs from the last one returned.
func (o *LifecycleOwner) PollControl() (ControlRecord, bool, error) {
	var rec ControlRecord
	err := recordReader{dir: o.dir, euid: o.euid, strict: true}.read(controlFileName, &rec)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return ControlRecord{}, false, nil
	case err != nil:
		return ControlRecord{}, false, err
	case rec.Version != LifecycleProtocolVersion || rec.Generation != o.generation:
		return ControlRecord{}, false, nil
	case rec == o.last:
		return ControlRecord{}, false, nil
	}
	o.last = rec
	return rec, true, nil
}

// LifecycleClient is the setup-side handle of the lifecycle protocol.
type LifecycleClient struct {
	dir   string
	euid  int
	owner OwnerRecord
}

// ConnectLifecycle waits, bounded by ctx, for a live owner record in dir.
func ConnectLifecycle(ctx context.Context, dir string) (*LifecycleClient, error) {
	euid := os.Geteuid()
	var lastErr error
	for {
		owner, err := liveOwner(dir, euid)
		switch {
		case owner != nil:
			return &LifecycleClient{dir: dir, euid: euid, owner: *owner}, nil
		case errors.Is(err, ErrLifecycleOwnerIncompatible):
			return nil, err
		case err != nil:
			lastErr = err
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return nil, fmt.Errorf("%w: %w", ErrLifecycleOwnerMissing, lastErr)
			}
			return nil, ErrLifecycleOwnerMissing
		case <-time.After(connectPollEvery):
		}
	}
}

// liveOwner returns the published owner when its process is still the one that
// wrote the record. A missing or stale record is (nil, nil).
func liveOwner(dir string, euid int) (*OwnerRecord, error) {
	var owner OwnerRecord
	err := recordReader{dir: dir, euid: euid, strict: true}.read(ownerFileName, &owner)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, err
	case owner.Version != LifecycleProtocolVersion:
		return nil, fmt.Errorf("%w: protocol version %d, want %d",
			ErrLifecycleOwnerIncompatible, owner.Version, LifecycleProtocolVersion)
	case !ownerAlive(owner):
		return nil, nil
	}
	return &owner, nil
}

func ownerAlive(o OwnerRecord) bool {
	if o.PID <= 0 || !processExists(o.PID) {
		return false
	}
	ticks := procStartTicks(o.PID)
	return o.StartTicks == "" || ticks == "" || ticks == o.StartTicks
}

// Send publishes the timer state for the connected owner.
func (c *LifecycleClient) Send(state LifecycleState, timeout, shutdownAction string) error {
	return writeRecord(c.dir, controlFileName, ControlRecord{
		Version:        LifecycleProtocolVersion,
		Generation:     c.owner.Generation,
		Timeout:        timeout,
		ShutdownAction: shutdownAction,
		State:          state,
	}, c.euid)
}

// ReadLifecycleTimeout leniently reads the timeout from the current control
// record, for deriving activity cadence. It reports false when there is none.
func ReadLifecycleTimeout(dir string) (time.Duration, bool) {
	var rec ControlRecord
	err := recordReader{dir: dir}.read(controlFileName, &rec)
	if err != nil || rec.Version != LifecycleProtocolVersion {
		return 0, false
	}
	d, err := time.ParseDuration(rec.Timeout)
	if err != nil || d <= 0 {
		return 0, rec.Timeout == ""
	}
	return d, true
}
