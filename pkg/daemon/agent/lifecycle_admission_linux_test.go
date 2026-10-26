//go:build linux

package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capacityPeerReport struct {
	lifecyclePeerIdentity
	Live int `json:"live"`
}

type capacityPeer struct {
	cmd     *exec.Cmd
	input   io.WriteCloser
	scanner *bufio.Scanner
	report  capacityPeerReport
}

func startCapacityPeer(t *testing.T, dir string, uid uint32, mode string) *capacityPeer {
	t.Helper()
	binary, err := os.Executable()
	require.NoError(t, err)
	// #nosec G204 -- re-execute the current test binary with fixed test selection.
	cmd := exec.Command(binary, "-test.run=^TestLifecycleCapacityPeerChild$")
	cmd.Env = append(os.Environ(), "DEVSY_CAPACITY_DIR="+dir, "DEVSY_CAPACITY_MODE="+mode)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uid, Gid: uid, Groups: []uint32{uid}},
		Pdeathsig:  syscall.SIGKILL,
	}
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	input, err := cmd.StdinPipe()
	require.NoError(t, err)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	peer := &capacityPeer{cmd: cmd, input: input, scanner: bufio.NewScanner(stdout)}
	t.Cleanup(func() { _ = peer.input.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	require.NoError(t, json.Unmarshal([]byte(readPeerLine(t, peer.scanner)), &peer.report))
	require.Equal(t, int(uid), peer.report.UID)
	require.Equal(t, int(uid), peer.report.GID)
	require.Equal(t, []int{int(uid)}, peer.report.Groups)
	require.Equal(t, cmd.Process.Pid, peer.report.PID)
	return peer
}

func (p *capacityPeer) close(t *testing.T) {
	t.Helper()
	_, err := io.WriteString(p.input, "exit\n")
	require.NoError(t, err)
	require.NoError(t, p.cmd.Wait())
}

func TestLowerTrustPendingClientsPreservePIDUIDAndOwnerFairness(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires real cross-UID clients under a root owner")
	}
	owner, dir := newTestOwner(t)
	monitor := &LifecycleMonitor{}
	monitor.ArmNow(time.Minute, time.Now())
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: monitor})
	pending := startCapacityPeer(t, dir, 65534, "pending")
	require.Equal(t, maxLifecyclePIDClients, pending.report.Live)
	allowed := startCapacityPeer(t, dir, 65534, "activity")
	require.Equal(t, maxLifecyclePIDClients, allowed.report.Live)
	denied := startCapacityPeer(t, dir, 65534, "activity")
	require.Zero(t, denied.report.Live, "UID exhaustion must not begin unregistered work")
	otherUID := startCapacityPeer(t, dir, 65533, "activity")
	require.Equal(t, maxLifecyclePIDClients, otherUID.report.Live)
	control := connectNow(t, dir)
	require.NoError(t, control.Send(LifecycleStateSetup, "1m", ""))
	require.NoError(t, control.Send(LifecycleStateReady, "1m", ""))
	for _, peer := range []*capacityPeer{pending, allowed, denied, otherUID} {
		peer.close(t)
	}
	require.Eventually(
		t,
		func() bool { return idleForAnHour(monitor).Expired },
		time.Second,
		time.Millisecond,
	)
}

func TestRootAdmissionSurvivesLowerTrustReleaseOnlySaturationAndDeath(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires real cross-UID clients under a root owner")
	}
	owner, dir := newTestOwner(t)
	monitor := &LifecycleMonitor{}
	monitor.ArmNow(time.Minute, time.Now())
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: monitor})
	var peers []*capacityPeer
	for i := range maxOtherLifecycleClients / maxLifecyclePIDClients {
		peer := startCapacityPeer(t, dir, uint32(65000+i), "release")
		require.Equal(t, maxLifecyclePIDClients, peer.report.Live)
		peers = append(peers, peer)
	}
	denied := startCapacityPeer(t, dir, 64999, "activity")
	require.Zero(t, denied.report.Live, "lower-trust saturation must not begin unregistered work")
	control := connectNow(t, dir)
	require.NoError(t, control.Send(LifecycleStateSetup, "1m", ""))
	require.NoError(t, control.Send(LifecycleStateReady, "1m", ""))
	assert.True(t, idleForAnHour(monitor).Expired, "release-only sockets do not retain work")
	require.NoError(t, peers[0].cmd.Process.Kill())
	require.Error(t, peers[0].cmd.Wait())
	owner.pruneDeadClients(monitor, time.Now())
	replacement := startCapacityPeer(t, dir, 64999, "activity")
	require.Equal(t, maxLifecyclePIDClients, replacement.report.Live)
	for _, peer := range append(peers[1:], denied, replacement) {
		peer.close(t)
	}
	require.Eventually(
		t,
		func() bool { return idleForAnHour(monitor).Expired },
		time.Second,
		time.Millisecond,
	)
}

func TestLifecycleCapacityPeerChild(t *testing.T) {
	mode := os.Getenv("DEVSY_CAPACITY_MODE")
	if mode == "" {
		return
	}
	dir := os.Getenv("DEVSY_CAPACITY_DIR")
	var clients []*LifecycleClient
	var pending []net.Conn
	if mode == "pending" {
		pending = openPendingCapacityConnections(t, dir)
	} else {
		clients = openRegisteredCapacityConnections(dir, mode)
	}
	defer func() {
		for _, conn := range pending {
			_ = conn.Close()
		}
		for _, client := range clients {
			_ = client.Close()
		}
	}()
	groups, err := os.Getgroups()
	require.NoError(t, err)
	report := capacityPeerReport{lifecyclePeerIdentity: lifecyclePeerIdentity{
		UID: os.Getuid(), GID: os.Getgid(), Groups: groups, PID: os.Getpid(),
	}, Live: len(clients)}
	if mode == "pending" {
		report.Live = countOpenPendingConnections(pending)
	}
	require.NoError(t, json.NewEncoder(os.Stdout).Encode(report))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if mode == "release" {
		go chatterReleaseClients(ctx, clients)
	}
	_, err = bufio.NewReader(os.Stdin).ReadString('\n')
	require.NoError(t, err)
}

func openPendingCapacityConnections(t *testing.T, dir string) []net.Conn {
	t.Helper()
	var conns []net.Conn
	for range maxLifecycleClients {
		conn, err := net.DialTimeout("unix", dir+"/"+socketFileName, time.Second)
		require.NoError(t, err)
		conns = append(conns, conn)
	}
	return conns
}

func countOpenPendingConnections(conns []net.Conn) int {
	live := 0
	for _, conn := range conns {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Millisecond))
		var buf [1]byte
		_, err := conn.Read(buf[:])
		if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			live++
		}
	}
	return live
}

func openRegisteredCapacityConnections(dir, mode string) []*LifecycleClient {
	var clients []*LifecycleClient
	state := LifecycleStateActivity
	if mode == "release" {
		state = LifecycleStateRelease
	}
	for range maxLifecycleClients {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		client, err := ConnectLifecycle(ctx, dir)
		cancel()
		if err != nil {
			break
		}
		if err := client.Send(state, "", ""); err != nil {
			_ = client.Close()
			break
		}
		clients = append(clients, client)
	}
	return clients
}

func chatterReleaseClients(ctx context.Context, clients []*LifecycleClient) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, client := range clients {
				_ = client.Send(LifecycleStateRelease, "", "")
			}
		}
	}
}
