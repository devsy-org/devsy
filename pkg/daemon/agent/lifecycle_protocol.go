package agent

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/tailscale/peercred"
)

const (
	LifecycleProtocolVersion = 3
	ownerFileName            = "owner.json"
	socketFileName           = "owner.sock"
	ackWaitTimeout           = 10 * time.Second
	ackPollEvery             = 100 * time.Millisecond
	maxRecordBytes           = 4096
	connectPollEvery         = 100 * time.Millisecond
	runtimeDirMode           = 0o755
	recordFileMode           = 0o644
)

var (
	ErrLifecycleOwnerMissing = errors.New(
		"container lifecycle owner unavailable; stop and start the workspace to replace its pod",
	)
	ErrLifecycleNotAcknowledged = errors.New(
		"container lifecycle owner did not acknowledge the update",
	)
	ErrLifecycleOwnerIncompatible = errors.New(
		"container lifecycle owner is incompatible; stop and start the workspace to replace its pod",
	)
)

type LifecycleState string

const (
	LifecycleStateSetup    LifecycleState = "setup"
	LifecycleStateReady    LifecycleState = "ready"
	LifecycleStateFailed   LifecycleState = "failed"
	LifecycleStateActivity LifecycleState = "activity"
	LifecycleStateRelease  LifecycleState = "release"
)

// OwnerRecord identifies the daemon instance, not a lease on work.
type OwnerRecord struct {
	Version    int    `json:"version"`
	Generation string `json:"generation"`
	PID        int    `json:"pid"`
	StartTicks string `json:"startTicks,omitempty"`
}

// ControlRecord belongs to one connection and operation. PID/start identity
// prevents inherited descriptors or PID reuse from orphaning setup protection.
type ControlRecord struct {
	Version        int            `json:"version"`
	Generation     string         `json:"generation"`
	ClientID       string         `json:"clientID"`
	Sequence       uint64         `json:"sequence"`
	PID            int            `json:"pid"`
	StartTicks     string         `json:"startTicks,omitempty"`
	Timeout        string         `json:"timeout"`
	ShutdownAction string         `json:"shutdownAction"`
	State          LifecycleState `json:"state"`
}

type AckRecord struct {
	Version    int    `json:"version"`
	Generation string `json:"generation"`
	ClientID   string `json:"clientID"`
	Sequence   uint64 `json:"sequence"`
	Error      string `json:"error,omitempty"`
}

func trustedOwner(uid, euid int) bool { return uid == euid || uid == 0 }

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
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return fmt.Errorf("write lifecycle record: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("publish lifecycle record: %w", err)
	}
	return nil
}

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

// NewLifecycleOwner binds before publishing discovery. All subsequent safety
// decisions and acknowledgements use the connection, never writable files.
func NewLifecycleOwner(dir string) (*LifecycleOwner, error) {
	euid := os.Geteuid()
	if err := prepareRuntimeDir(dir, euid); err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", filepath.Join(dir, socketFileName))
	if err != nil {
		return nil, err
	}
	// #nosec G302 -- container-local activity accepts workspace users; controls require peer ownership.
	if err := os.Chmod(filepath.Join(dir, socketFileName), 0o666); err != nil {
		_ = listener.Close()
		return nil, err
	}
	o := &LifecycleOwner{
		euid: euid, generation: rand.Text(), listener: listener,
		clients: make(map[net.Conn]*lifecycleSession),
	}
	o.writeAck = writeSocketAck
	err = writeRecord(dir, ownerFileName, OwnerRecord{
		Version: LifecycleProtocolVersion, Generation: o.generation,
		PID: os.Getpid(), StartTicks: procStartTicks(os.Getpid()),
	}, euid)
	if err != nil {
		_ = o.Close()
		return nil, err
	}
	return o, nil
}

func createRuntimeDir(dir string, euid int) error {
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
	return checkDir(dir, euid)
}

func prepareRuntimeDir(dir string, euid int) error {
	if err := createRuntimeDir(dir, euid); err != nil {
		return err
	}
	for _, name := range []string{ownerFileName, socketFileName} {
		if err := os.Remove(
			filepath.Join(dir, name),
		); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func requireOwnedDir(dir string, euid int) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("lifecycle runtime path %s is not a directory", dir)
	}
	if uid, ok := fileOwner(fi); ok && uid != euid {
		return fmt.Errorf("lifecycle runtime dir %s is owned by uid %d, not %d", dir, uid, euid)
	}
	return nil
}

func (o *LifecycleOwner) Generation() string { return o.generation }

// LifecycleClient never reconnects silently: losing protection cancels the work.
// A fresh operation must explicitly connect to the current owner instance.
type LifecycleClient struct {
	conn       net.Conn
	owner      OwnerRecord
	clientID   string
	mu         sync.Mutex
	sequence   uint64
	ackTimeout time.Duration
	acks       chan AckRecord
	done       chan struct{}
	closeOnce  sync.Once
}

func ConnectLifecycle(ctx context.Context, dir string) (*LifecycleClient, error) {
	for {
		owner, err := liveOwner(dir, os.Geteuid())
		if err != nil {
			return nil, err
		}
		if owner != nil {
			return connectOwner(ctx, dir, *owner)
		}
		select {
		case <-ctx.Done():
			return nil, ErrLifecycleOwnerMissing
		case <-time.After(connectPollEvery):
		}
	}
}

func connectOwner(ctx context.Context, dir string, owner OwnerRecord) (*LifecycleClient, error) {
	ctx, cancel := context.WithTimeout(ctx, ackWaitTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, socketFileName))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLifecycleOwnerMissing, err)
	}
	creds, err := peercred.Get(conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	uid, ok := creds.UserID()
	pid, hasPID := creds.PID()
	if !ok || (uid != "0" && uid != strconv.Itoa(os.Geteuid())) || (hasPID && pid != owner.PID) {
		_ = conn.Close()
		return nil, fmt.Errorf(
			"%w: socket owner differs from discovery record",
			ErrLifecycleOwnerIncompatible,
		)
	}
	c := &LifecycleClient{
		conn: conn, owner: owner, clientID: rand.Text(), ackTimeout: ackWaitTimeout,
		acks: make(chan AckRecord, 8), done: make(chan struct{}),
	}
	go c.readAcks()
	return c, nil
}

func liveOwner(dir string, euid int) (*OwnerRecord, error) {
	var owner OwnerRecord
	err := recordReader{dir: dir, euid: euid, strict: true}.read(ownerFileName, &owner)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrLifecycleOwnerMissing, err)
	case owner.Version != LifecycleProtocolVersion:
		return nil, fmt.Errorf(
			"%w: protocol version %d, want %d",
			ErrLifecycleOwnerIncompatible,
			owner.Version,
			LifecycleProtocolVersion,
		)
	case !ownerAlive(owner):
		return nil, ErrLifecycleOwnerMissing
	}
	return &owner, nil
}

func ownerAlive(o OwnerRecord) bool { return o.PID > 0 && sameProcess(o.PID, o.StartTicks) }

func sameProcess(pid int, startTicks string) bool {
	if !processExists(pid) {
		return false
	}
	ticks := procStartTicks(pid)
	return startTicks == "" || ticks == "" || ticks == startTicks
}

func (c *LifecycleClient) readAcks() {
	defer func() { _ = c.Close() }()
	scanner := bufio.NewScanner(c.conn)
	scanner.Buffer(make([]byte, 512), maxRecordBytes)
	for scanner.Scan() {
		var ack AckRecord
		if json.Unmarshal(scanner.Bytes(), &ack) != nil ||
			!c.validAck(ack) {
			return
		}
		select {
		case c.acks <- ack:
		case <-c.done:
			return
		default:
			return
		}
	}
}

func (c *LifecycleClient) Done() <-chan struct{} { return c.done }

func (c *LifecycleClient) Close() error {
	var err error
	c.closeOnce.Do(func() { err = c.conn.Close(); close(c.done) })
	return err
}

// ProtectedContext aborts work on socket loss instead of leaving it untracked.
func (c *LifecycleClient) ProtectedContext(
	parent context.Context,
) (context.Context, context.CancelCauseFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	go func() {
		select {
		case <-parent.Done():
			cancel(context.Cause(parent))
			_ = c.Close()
		case <-c.done:
			cancel(ErrLifecycleOwnerMissing)
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// Send retries the same operation while the connection survives. The owner's
// cached result makes acknowledgement recovery independent of applying state.
func (c *LifecycleClient) Send(
	state LifecycleState,
	timeout, shutdownAction string,
) (sendErr error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer func() {
		if sendErr != nil {
			_ = c.Close()
		}
	}()
	c.sequence++
	rec := ControlRecord{
		Version: LifecycleProtocolVersion, Generation: c.owner.Generation,
		ClientID: c.clientID, Sequence: c.sequence,
		PID: os.Getpid(), StartTicks: procStartTicks(os.Getpid()),
		State: state, Timeout: timeout, ShutdownAction: shutdownAction,
	}
	return c.sendRecord(rec)
}

func (c *LifecycleClient) sendRecord(rec ControlRecord) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if len(raw) >= maxRecordBytes {
		return errors.New("lifecycle message exceeds size limit")
	}
	raw = append(raw, '\n')
	deadline := time.Now().Add(c.ackTimeout)
	for {
		ack, err := c.exchange(raw, rec.Sequence, deadline)
		if err != nil {
			return err
		}
		if ack == nil {
			continue
		}
		if ack.Error != "" {
			return fmt.Errorf("container lifecycle owner rejected the update: %s", ack.Error)
		}
		return nil
	}
}

func (c *LifecycleClient) exchange(
	raw []byte,
	sequence uint64,
	deadline time.Time,
) (*AckRecord, error) {
	if err := c.conn.SetWriteDeadline(deadline); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLifecycleOwnerMissing, err)
	}
	if _, err := c.conn.Write(raw); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLifecycleOwnerMissing, err)
	}
	return c.awaitAck(sequence, deadline)
}

func (c *LifecycleClient) awaitAck(sequence uint64, deadline time.Time) (*AckRecord, error) {
	timer := time.NewTimer(min(ackPollEvery, max(time.Until(deadline), 0)))
	defer timer.Stop()
	for {
		select {
		case ack := <-c.acks:
			if ack.Sequence == sequence {
				return &ack, nil
			}
		case <-c.done:
			return nil, ErrLifecycleOwnerMissing
		case <-timer.C:
			if !time.Now().Before(deadline) {
				return nil, ErrLifecycleNotAcknowledged
			}
			return nil, nil
		}
	}
}

func (c *LifecycleClient) validAck(ack AckRecord) bool {
	return ack.Version == LifecycleProtocolVersion &&
		ack.Generation == c.owner.Generation &&
		ack.ClientID == c.clientID
}
