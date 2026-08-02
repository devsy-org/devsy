package gpg

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listenUnixSocket creates a real Unix domain socket at path, registers it
// for cleanup, and returns it. The caller must run this on the test
// goroutine (or join the calling goroutine) before the test returns, so the
// listener is registered before t.Cleanup fires and a net.Listen failure is
// observed by the test rather than silently dropped in a detached goroutine.
func listenUnixSocket(t *testing.T, path string) net.Listener {
	t.Helper()
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// shortSocketDir returns a short-enough temp dir for a unix socket path:
// t.TempDir()'s nested path can exceed the ~104-byte sun_path limit on
// macOS/BSD, so this creates directly under os.TempDir() instead.
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

	listenerReady := make(chan struct{})
	go func() {
		defer close(listenerReady)
		time.Sleep(50 * time.Millisecond)
		listenUnixSocket(t, socketPath)
	}()
	defer func() { <-listenerReady }()

	start := time.Now()
	err := g.claimForwardedSocket(context.Background())
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 2*time.Second, "must return shortly after the socket appears")
	// claimForwardedSocket's final step (chown to the caller's uid) requires
	// sudo, which may not be passwordless in this environment; only the
	// socket-appear wait itself is under test here, so a chown-only failure
	// is tolerated, but any error about the socket not appearing is not.
	if err != nil {
		assert.NotContains(t, err.Error(), "did not appear")
		assert.NotContains(t, err.Error(), "not a unix socket")
	}
}

func TestClaimForwardedSocket_TimesOutWhenSocketNeverAppears(t *testing.T) {
	g := &GPGConf{SocketPath: filepath.Join(t.TempDir(), "S.gpg-agent")}

	// The backoff's own worst case (15 steps, 200ms*1.5^n capped at 2s, plus
	// up to 10% jitter per step) is a bit over 20s, so the timeout used to
	// wait it out must exceed that, not sit just under it, or this assertion
	// itself becomes flaky on the exact path it verifies.
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
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
	assert.Less(t, elapsed, time.Second, "must fail immediately, not retry against a non-socket path")
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
