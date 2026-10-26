package agent

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type lifecycleDeadlineProbe struct {
	opened time.Time
	closed <-chan time.Time
}

func TestUnprotectedChatterAndExactRetriesCannotExtendConnectionLifetime(t *testing.T) {
	owner, dir := newTestOwner(t)
	monitor := &LifecycleMonitor{}
	monitor.ArmNow(time.Minute, time.Now())
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: monitor})
	fresh := newLifecycleDeadlineProbe(t, owner, dir, "fresh")
	retry := newLifecycleDeadlineProbe(t, owner, dir, "retry")
	pending := newLifecycleDeadlineProbe(t, owner, dir, "pending")
	protected := newLifecycleDeadlineProbe(t, owner, dir, "invalid-ready")
	slow := newLifecycleDeadlineProbe(t, owner, dir, "slowloris")
	for _, probe := range []*lifecycleDeadlineProbe{fresh, retry, pending, slow} {
		select {
		case closed := <-probe.closed:
			assert.WithinDuration(t, probe.opened.Add(ackWaitTimeout), closed, 2*time.Second)
		case <-time.After(ackWaitTimeout + 2*time.Second):
			t.Fatal("unprotected connection outlived its original admission deadline")
		}
	}
	select {
	case <-protected.closed:
		t.Fatal("a failed policy update removed an earlier valid setup hold")
	default:
	}
	assert.False(t, idleForAnHour(monitor).Expired, "live protected work has no connection lease")
}

func newLifecycleDeadlineProbe(
	t *testing.T,
	owner *LifecycleOwner,
	dir, mode string,
) *lifecycleDeadlineProbe {
	t.Helper()
	opened := time.Now()
	conn, err := net.DialTimeout("unix", filepath.Join(dir, socketFileName), time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	record := ControlRecord{
		Version: LifecycleProtocolVersion, Generation: owner.generation,
		ClientID: mode, Sequence: 1, PID: os.Getpid(), StartTicks: procStartTicks(os.Getpid()),
		State: LifecycleStateRelease,
	}
	if mode != "pending" && mode != "slowloris" {
		initializeDeadlineProbe(t, conn, &record, mode)
	}
	closed := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, conn); closed <- time.Now(); close(done) }()
	if mode == "fresh" || mode == "retry" {
		go chatterLifecycleWire(conn, record, mode == "retry", done)
	}
	if mode == "slowloris" {
		go chatterIncompleteLifecycleFrame(conn, done)
	}
	return &lifecycleDeadlineProbe{opened: opened, closed: closed}
}

func initializeDeadlineProbe(t *testing.T, conn net.Conn, record *ControlRecord, mode string) {
	t.Helper()
	if mode == "invalid-ready" {
		record.State, record.Timeout = LifecycleStateSetup, "1m"
	}
	require.NoError(t, json.NewEncoder(conn).Encode(record))
	var ack AckRecord
	require.NoError(t, json.NewDecoder(conn).Decode(&ack))
	require.Empty(t, ack.Error)
	if mode != "invalid-ready" {
		return
	}
	record.State, record.Timeout, record.Sequence = LifecycleStateReady, "invalid", 2
	require.NoError(t, json.NewEncoder(conn).Encode(record))
	require.NoError(t, json.NewDecoder(conn).Decode(&ack))
	require.NotEmpty(t, ack.Error, "the policy transition must actually be rejected")
}

func chatterLifecycleWire(conn net.Conn, record ControlRecord, retry bool, done <-chan struct{}) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	encoder := json.NewEncoder(conn)
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if !retry {
				record.Sequence++
			}
			if err := encoder.Encode(record); err != nil {
				return
			}
		}
	}
}

func TestMalformedLifecycleFrameDoesNotStopTheOwner(t *testing.T) {
	owner, dir := newTestOwner(t)
	monitor := &LifecycleMonitor{}
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: monitor})
	conn, err := net.DialTimeout("unix", filepath.Join(dir, socketFileName), time.Second)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = io.WriteString(conn, "{\n")
	require.NoError(t, err)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	var buf [1]byte
	_, err = conn.Read(buf[:])
	assert.ErrorIs(t, err, io.EOF)
	client := connectNow(t, dir)
	require.NoError(t, client.Send(LifecycleStateSetup, "1m", ""))
	require.NoError(t, client.Send(LifecycleStateReady, "1m", ""))
}

func chatterIncompleteLifecycleFrame(conn net.Conn, done <-chan struct{}) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if _, err := io.WriteString(conn, " "); err != nil {
				return
			}
		}
	}
}
