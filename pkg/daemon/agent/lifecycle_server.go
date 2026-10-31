package agent

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/tailscale/peercred"
)

const (
	maxLifecycleClients      = 128
	reservedLifecycleClients = 32
	maxOtherLifecycleClients = maxLifecycleClients - reservedLifecycleClients
	maxLifecycleUIDClients   = 32
	maxLifecyclePIDClients   = 16
)

var errLifecycleClientLimit = errors.New("lifecycle owner connection limit reached")

type lifecycleSession struct {
	conn         net.Conn
	pid          int
	startTicks   string
	uid          string
	privileged   bool
	sharedSlot   bool
	protected    bool
	readDeadline time.Time
	clientID     string
	protectionID string
	applied      ControlRecord
	result       AckRecord
}

// LifecycleOwner keeps live work and applied operations in memory. Discovery
// and policy files are never evidence that a registered writer is healthy.
type LifecycleOwner struct {
	euid         int
	generation   string
	listener     net.Listener
	mu           sync.Mutex
	closed       bool
	clients      map[net.Conn]*lifecycleSession
	otherClients int
	clientsByUID map[string]int
	clientsByPID map[int]int
	writeAck     func(net.Conn, AckRecord) error
}

func (o *LifecycleOwner) Close() error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return nil
	}
	o.closed = true
	conns := make([]net.Conn, 0, len(o.clients))
	for conn := range o.clients {
		conns = append(conns, conn)
	}
	o.mu.Unlock()
	err := o.listener.Close()
	for _, conn := range conns {
		_ = conn.Close()
	}
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (o *LifecycleOwner) Serve(ctx context.Context, monitor *LifecycleMonitor) error {
	stop := context.AfterFunc(ctx, func() { _ = o.Close() })
	defer stop()
	var workers sync.WaitGroup
	defer func() { _ = o.Close(); workers.Wait() }()
	for {
		conn, err := o.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept lifecycle client: %w", err)
		}
		session, err := o.addClient(conn)
		if err != nil {
			_ = conn.Close()
			continue
		}
		workers.Go(func() { o.serveClient(session, monitor) })
	}
}

func (o *LifecycleOwner) addClient(conn net.Conn) (*lifecycleSession, error) {
	pid, uid, err := readLifecyclePeer(conn)
	if err != nil {
		return nil, err
	}
	privileged := uid == "0" || uid == strconv.Itoa(o.euid)
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || !o.canAdmit(uid, pid, privileged) {
		return nil, errLifecycleClientLimit
	}
	ticks, err := processStartIdentity(pid)
	if err != nil {
		return nil, err
	}
	s := &lifecycleSession{
		conn: conn, pid: pid, uid: uid, privileged: privileged, sharedSlot: !privileged,
		startTicks: ticks, protectionID: rand.Text(),
		readDeadline: time.Now().Add(ackWaitTimeout),
	}
	o.clients[conn] = s
	o.countClient(s)
	return s, nil
}

func readLifecyclePeer(conn net.Conn) (int, string, error) {
	creds, err := peercred.Get(conn)
	if err != nil {
		return 0, "", err
	}
	pid, hasPID := creds.PID()
	uid, hasUID := creds.UserID()
	if !hasPID || !hasUID || pid <= 0 {
		return 0, "", errors.New("lifecycle peer identity is unavailable")
	}
	return pid, uid, nil
}

func (o *LifecycleOwner) removeClient(s *lifecycleSession, monitor *LifecycleMonitor) {
	_ = s.conn.Close()
	o.mu.Lock()
	o.removeClientLocked(s)
	id := s.protectionID
	o.mu.Unlock()
	monitor.Release(id, time.Now())
}

func (o *LifecycleOwner) pruneDeadClients(monitor *LifecycleMonitor, now time.Time) {
	o.mu.Lock()
	dead := make([]*lifecycleSession, 0)
	for _, s := range o.clients {
		if !sameProcess(s.pid, s.startTicks) {
			dead = append(dead, s)
		}
	}
	for _, s := range dead {
		o.removeClientLocked(s)
		monitor.Release(s.protectionID, now)
	}
	o.mu.Unlock()
	for _, s := range dead {
		_ = s.conn.Close()
	}
}

func (s *lifecycleSession) validateIdentity(rec ControlRecord) error {
	if rec.ClientID == "" || rec.Sequence == 0 || rec.PID != s.pid ||
		(rec.StartTicks != "" && s.startTicks != "" && rec.StartTicks != s.startTicks) {
		return errors.New("lifecycle client identity does not match its connection")
	}
	return nil
}

func (o *LifecycleOwner) authorize(s *lifecycleSession, rec ControlRecord) error {
	if rec.State == LifecycleStateActivity || rec.State == LifecycleStateRelease {
		return nil
	}
	if s.privileged {
		return nil
	}
	return errors.New("lifecycle policy update requires the daemon's user or root")
}

func (o *LifecycleOwner) validate(s *lifecycleSession, rec ControlRecord) error {
	if rec.Version != LifecycleProtocolVersion || rec.Generation != o.generation {
		return ErrLifecycleOwnerIncompatible
	}
	if err := s.validateIdentity(rec); err != nil {
		return err
	}
	if err := o.authorize(s, rec); err != nil {
		return err
	}
	return o.validateSession(s, rec)
}

func (o *LifecycleOwner) validateSession(s *lifecycleSession, rec ControlRecord) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.clients[s.conn] != s {
		return ErrLifecycleOwnerMissing
	}
	if s.clientID != "" && s.clientID != rec.ClientID {
		return errors.New("lifecycle connection changed client identity")
	}
	s.clientID = rec.ClientID
	return o.classifyActivity(s, rec.State)
}

func (o *LifecycleOwner) serveClient(s *lifecycleSession, monitor *LifecycleMonitor) {
	defer o.removeClient(s, monitor)
	// An unauthenticated idle socket is not protected work and must not consume
	// a connection slot indefinitely. Registered live work has no time lease.
	_ = s.conn.SetReadDeadline(s.readDeadline)
	scanner := bufio.NewScanner(s.conn)
	scanner.Buffer(make([]byte, 512), maxRecordBytes)
	// Applied results are connection-owned and retained independently of ACKs.
	for scanner.Scan() {
		var rec ControlRecord
		if json.Unmarshal(scanner.Bytes(), &rec) != nil {
			return
		}
		if err := o.validate(s, rec); err != nil {
			return
		}
		result, err := o.applyOperation(s, rec, monitor)
		if err != nil {
			return
		}
		if err := o.writeAck(s.conn, result); err != nil {
			log.Debugf("lifecycle ACK delivery failed; retaining applied result for retry: %v", err)
		}
	}
}

func (o *LifecycleOwner) applyOperation(
	s *lifecycleSession,
	rec ControlRecord,
	monitor *LifecycleMonitor,
) (AckRecord, error) {
	if rec == s.applied {
		return s.result, nil
	}
	if rec.Sequence != s.applied.Sequence+1 {
		return AckRecord{}, errors.New("lifecycle sequence is not consecutive")
	}
	state := rec
	state.ClientID = s.protectionID
	now := time.Now()
	_, applyErr := monitor.Apply(state, now)
	if applyErr == nil {
		s.transitionProtection(rec.State, now)
	}
	s.applied = rec
	s.result = AckRecord{
		Version: LifecycleProtocolVersion, Generation: o.generation,
		ClientID: rec.ClientID, Sequence: rec.Sequence,
	}
	if applyErr != nil {
		s.result.Error = applyErr.Error()
	}
	return s.result, nil
}

func writeSocketAck(conn net.Conn, ack AckRecord) error {
	if err := conn.SetWriteDeadline(time.Now().Add(ackPollEvery)); err != nil {
		return err
	}
	return json.NewEncoder(conn).Encode(ack)
}

func (o *LifecycleOwner) canAdmit(uid string, pid int, privileged bool) bool {
	if len(o.clients) >= maxLifecycleClients {
		return false
	}
	return privileged || (o.otherClients < maxOtherLifecycleClients &&
		o.clientsByUID[uid] < maxLifecycleUIDClients &&
		o.clientsByPID[pid] < maxLifecyclePIDClients)
}

func (o *LifecycleOwner) countClient(s *lifecycleSession) {
	if s.sharedSlot {
		o.otherClients++
	}
	if s.privileged {
		return
	}
	if o.clientsByUID == nil {
		o.clientsByUID = make(map[string]int)
		o.clientsByPID = make(map[int]int)
	}
	o.clientsByUID[s.uid]++
	o.clientsByPID[s.pid]++
}

func (o *LifecycleOwner) removeClientLocked(s *lifecycleSession) {
	if _, present := o.clients[s.conn]; !present {
		return
	}
	delete(o.clients, s.conn)
	if s.sharedSlot {
		o.otherClients--
	}
	if s.privileged {
		return
	}
	o.clientsByUID[s.uid]--
	o.clientsByPID[s.pid]--
	if o.clientsByUID[s.uid] == 0 {
		delete(o.clientsByUID, s.uid)
	}
	if o.clientsByPID[s.pid] == 0 {
		delete(o.clientsByPID, s.pid)
	}
}

func (s *lifecycleSession) transitionProtection(state LifecycleState, now time.Time) {
	protected := state == LifecycleStateSetup || state == LifecycleStateActivity
	if protected == s.protected {
		return
	}
	s.protected = protected
	s.readDeadline = time.Time{}
	if !protected {
		s.readDeadline = now.Add(ackWaitTimeout)
	}
	_ = s.conn.SetReadDeadline(s.readDeadline)
}

func (o *LifecycleOwner) classifyActivity(s *lifecycleSession, state LifecycleState) error {
	if s.sharedSlot || (state != LifecycleStateActivity && state != LifecycleStateRelease) {
		return nil
	}
	if o.otherClients >= maxOtherLifecycleClients {
		return errLifecycleClientLimit
	}
	s.sharedSlot = true
	o.otherClients++
	return nil
}
