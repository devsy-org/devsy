//go:build linux || darwin || unix

package gpg

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func listenUnixSocket(path string) (net.Listener, error) {
	return net.Listen("unix", path)
}

func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gpgsock")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestSetupGpgConf_WritesRequiredDirectives(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".gnupg"), 0o700))

	require.NoError(t, SetupGpgConf())

	got := readConf(t, gpgConfigPath())
	for _, d := range gpgConfDirectives {
		assert.Contains(t, got, d, "gpg.conf must enable %q for forwarding", d)
	}
	assert.Contains(t, gpgConfDirectives, "no-autostart")
}

func TestSetupGpgConf_Idempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".gnupg"), 0o700))

	require.NoError(t, SetupGpgConf())
	require.NoError(t, SetupGpgConf())

	for _, d := range gpgConfDirectives {
		assert.Equal(t, 1, strings.Count(readConf(t, gpgConfigPath()), d+"\n"),
			"directive %q should not be duplicated on repeated setup", d)
	}
}

func TestSetupGpgConf_PreservesExistingDirectives(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".gnupg"), 0o700))

	require.NoError(t, os.WriteFile(gpgConfigPath(), []byte("use-agent\n"), 0o600))
	require.NoError(t, SetupGpgConf())

	got := readConf(t, gpgConfigPath())
	assert.Equal(t, 1, strings.Count(got, "use-agent\n"))
	assert.Contains(t, got, "no-autostart")
}

func TestSetupGpgConf_ExistingFileWithoutTrailingNewline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".gnupg"), 0o700))

	require.NoError(t, os.WriteFile(gpgConfigPath(), []byte("use-agent"), 0o600))
	require.NoError(t, SetupGpgConf())

	got := readConf(t, gpgConfigPath())
	assert.NotContains(t, got, "use-agentno-autostart", "directives must not be concatenated")
	for _, line := range []string{"use-agent", "no-autostart"} {
		assert.True(t, containsDirective(got, line), "missing directive %q in %q", line, got)
	}
}

func TestClaimForwardedSocket_StopsPollingAsSoonAsSocketAppears(t *testing.T) {
	socketPath := filepath.Join(shortSocketDir(t), "S.gpg-agent")
	g := &GPGConf{SocketPath: socketPath}

	var listener net.Listener
	var listenErr error
	listenerReady := make(chan struct{})
	go func() {
		defer close(listenerReady)
		time.Sleep(50 * time.Millisecond)
		listener, listenErr = listenUnixSocket(socketPath)
	}()
	defer func() {
		<-listenerReady
		require.NoError(t, listenErr)
		_ = listener.Close()
	}()

	start := time.Now()
	err := g.claimForwardedSocket(context.Background())
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 2*time.Second, "must return shortly after the socket appears")
	if err != nil {
		var exitErr *exec.ExitError
		var execErr *exec.Error
		assert.True(t, errors.As(err, &exitErr) || errors.As(err, &execErr),
			"only a sudo chown failure is tolerated here, got: %v", err)
	}
}

func TestClaimForwardedSocket_TimesOutWhenSocketNeverAppears(t *testing.T) {
	g := &GPGConf{SocketPath: filepath.Join(t.TempDir(), "S.gpg-agent")}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	err := g.claimForwardedSocket(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not appear")
}

func TestClaimForwardedSocket_RejectsNonSocketPath(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "S.gpg-agent")
	require.NoError(t, os.WriteFile(socketPath, []byte("not a socket"), 0o600))
	g := &GPGConf{SocketPath: socketPath}

	start := time.Now()
	err := g.claimForwardedSocket(context.Background())
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a unix socket")
	assert.Less(
		t,
		elapsed,
		time.Second,
		"must fail immediately, not retry against a non-socket path",
	)
}

func TestClaimForwardedSocket_FailsFastOnPermissionError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses directory permission checks")
	}

	parent := t.TempDir()
	socketPath := filepath.Join(parent, "S.gpg-agent")
	require.NoError(t, os.Chmod(parent, 0o000))
	//nolint:gosec // restore the temp dir's own perms so t.TempDir() cleanup can remove it
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	g := &GPGConf{SocketPath: socketPath}

	start := time.Now()
	err := g.claimForwardedSocket(context.Background())
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "did not appear",
		"a permission error must not be reported as the socket never appearing")
	assert.Less(
		t,
		elapsed,
		time.Second,
		"must fail immediately, not retry against a permission error",
	)
}

func TestClaimForwardedSocket_RespectsContextCancellation(t *testing.T) {
	g := &GPGConf{SocketPath: filepath.Join(t.TempDir(), "S.gpg-agent")}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := g.claimForwardedSocket(ctx)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled),
		"error must wrap context.Canceled, not just any error: got %v", err)
	assert.Less(t, elapsed, time.Second, "cancelled context must not wait out the full backoff")
}

func readConf(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test path is created by the test
	require.NoError(t, err)
	return string(b)
}
