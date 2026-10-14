package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	sshBannerPrefix = "SSH-"

	bannerAttemptTimeout = 3 * time.Second
	bannerRetryGap       = time.Second
	loopbackAttempts     = 20
	vmIPAttempts         = 40
)

var daemonIDPattern = regexp.MustCompile(`^[A-Za-z0-9:._-]{8,128}$`)

// probeSSHBanner dials addr and succeeds only when the peer sends an SSH
// identification line. A TCP accept alone proves nothing: the WSL localhost
// relay accepts connections before sshd listens.
func probeSSHBanner(ctx context.Context, dialer Dialer, addr string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	// Closing the connection on cancellation unblocks a read that has no
	// deadline support (the real deadline below covers ordinary sockets).
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	return readSSHBanner(conn, addr)
}

// readSSHBanner reads exactly len("SSH-") bytes, tolerating partial reads.
func readSSHBanner(r io.Reader, addr string) error {
	got := make([]byte, len(sshBannerPrefix))
	n, err := io.ReadFull(r, got)
	if err != nil {
		return fmt.Errorf("read banner from %s after %d bytes: %w", addr, n, err)
	}
	if string(got) != sshBannerPrefix {
		return fmt.Errorf("%s answered %q, not an SSH identification line", addr, got)
	}
	return nil
}

// sshCandidate is one address to probe and how many one-second retries it gets.
type sshCandidate struct {
	host     string
	attempts int
}

// waitForBanner retries probeSSHBanner on one candidate.
func waitForBanner(ctx context.Context, deps *Dependencies, addr string, attempts int) error {
	var last error
	for i := range attempts {
		if last = probeSSHBanner(ctx, deps.Net, addr, bannerAttemptTimeout); last == nil {
			return nil
		}
		if i == attempts-1 {
			break
		}
		if err := deps.Clock.Sleep(ctx, bannerRetryGap); err != nil {
			return err
		}
	}
	return last
}

// selectSSHAddress returns the first candidate that presents an SSH banner.
// The error lists every attempt so a failure is diagnosable.
func selectSSHAddress(
	ctx context.Context,
	deps *Dependencies,
	candidates []sshCandidate,
) (string, error) {
	var failures []error
	for _, c := range candidates {
		err := waitForBanner(ctx, deps, net.JoinHostPort(c.host, sshPort), c.attempts)
		if err == nil {
			return c.host, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		failures = append(failures, err)
	}
	return "", errors.Join(failures...)
}

// sshCandidates orders loopback first, then the parsed WSL VM addresses.
func sshCandidates(vmIPs []string) []sshCandidate {
	list := []sshCandidate{{host: "127.0.0.1", attempts: loopbackAttempts}}
	for _, ip := range vmIPs {
		list = append(list, sshCandidate{host: ip, attempts: vmIPAttempts})
	}
	return list
}

// parseVMAddresses extracts usable IPv4 addresses from `hostname -I` output,
// dropping loopback, link-local and unspecified entries and duplicates.
func parseVMAddresses(output string) []string {
	var ips []string
	for field := range strings.FieldsSeq(output) {
		ip, err := netip.ParseAddr(field)
		if err != nil || !ip.Is4() {
			continue
		}
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			continue
		}
		if s := ip.String(); !slices.Contains(ips, s) {
			ips = append(ips, s)
		}
	}
	return ips
}

// parseDaemonID validates `docker info --format {{.ID}}` output.
func parseDaemonID(stdout string) (string, error) {
	id := strings.TrimSpace(stdout)
	if !daemonIDPattern.MatchString(id) {
		return "", fmt.Errorf("docker info returned no usable daemon ID (%q)", tailBytes(id, 64))
	}
	return id, nil
}

// compareDaemonIDs requires the daemon reached over SSH to be the WSL daemon.
func compareDaemonIDs(wslID, sshID string) error {
	if wslID == "" || sshID == "" {
		return errors.New("daemon ID is empty")
	}
	if wslID != sshID {
		return fmt.Errorf(
			"daemon ID via SSH (%s) differs from the WSL daemon ID (%s)",
			sshID,
			wslID,
		)
	}
	return nil
}

// requireDeadPipeFailure is the negative oracle: with DOCKER_HOST pointing at a
// pipe nobody serves, docker info must fail. Success means some other daemon is
// reachable from the process environment and the suite could pass for the wrong
// reason. A timeout or start failure proves nothing, so they fail the oracle too.
func requireDeadPipeFailure(res CommandResult, runErr error) error {
	switch {
	case runErr != nil:
		return fmt.Errorf("could not run the dead-pipe oracle: %w", runErr)
	case res.TimedOut:
		return errors.New("dead-pipe oracle timed out before docker info failed")
	case res.ExitCode == 0:
		return errors.New(
			"a Docker daemon is reachable without SSH; the regression oracle would be void",
		)
	}
	return nil
}
