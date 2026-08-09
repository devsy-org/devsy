package framework

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/wait"
)

// exitError runs a shell command that exits with the given code and returns
// the resulting *exec.ExitError.
func exitError(t *testing.T, code string) *exec.ExitError {
	t.Helper()
	// #nosec G204 -- test helper with controlled exit code argument
	err := exec.Command("sh", "-c", "exit "+code).Run()
	require.Error(t, err)
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return exitErr
}

func TestIsRetryableSSHError_ConnectionRefused(t *testing.T) {
	assert.True(
		t,
		isRetryableSSHError(
			exitError(t, "1"),
			"ssh: connect to host 127.0.0.1 port 22: Connection refused",
		),
	)
}

func TestIsRetryableSSHError_ConnectionReset(t *testing.T) {
	assert.True(t, isRetryableSSHError(exitError(t, "1"), "Connection reset by peer"))
}

func TestIsRetryableSSHError_TunnelToContainer_NotRetryable(t *testing.T) {
	assert.False(t, isRetryableSSHError(exitError(t, "1"), "error: tunnel to container failed"))
}

func TestIsRetryableSSHError_RemoteCommandFailure(t *testing.T) {
	// The devsy CLI wraps all command failures with "tunnel to container:" in stderr.
	// This must NOT be treated as a transient SSH error.
	assert.False(
		t,
		isRetryableSSHError(
			exitError(t, "1"),
			"tunnel to container: run in container: ssh session: Process exited with status 1",
		),
	)
}

func TestIsRetryableSSHError_ConnectionTimedOut(t *testing.T) {
	assert.True(
		t,
		isRetryableSSHError(
			exitError(t, "1"),
			"ssh: connect to host 10.0.0.1 port 22: Connection timed out",
		),
	)
}

func TestIsRetryableSSHError_WorkspaceNotFound(t *testing.T) {
	assert.True(
		t,
		isRetryableSSHError(
			exitError(t, "1"),
			"workspace not found for args: [/tmp/temp-XXXXXXX]",
		),
	)
}

func TestIsRetryableSSHError_ForkExecPermissionDenied(t *testing.T) {
	assert.True(
		t,
		isRetryableSSHError(
			exitError(t, "1"),
			"start command: fork/exec /usr/bin/bash: permission denied",
		),
	)
}

func TestIsRetryableSSHError_ForkExecWithoutPermissionDenied_NotRetryable(t *testing.T) {
	assert.False(
		t,
		isRetryableSSHError(
			exitError(t, "1"),
			"start command: fork/exec /usr/bin/bash: no such file or directory",
		),
	)
}

func TestIsRetryableSSHError_ExitCode1_NoSSHPattern(t *testing.T) {
	// Remote command failure (e.g. cat on missing file) — should NOT be retried.
	assert.False(
		t,
		isRetryableSSHError(
			exitError(t, "1"),
			"cat: /home/user/post-attach.out: No such file or directory",
		),
	)
}

func TestIsRetryableSSHError_ExitCode1_EmptyStderr(t *testing.T) {
	assert.False(t, isRetryableSSHError(exitError(t, "1"), ""))
}

func TestIsRetryableSSHError_ExitStatus10(t *testing.T) {
	assert.False(t, isRetryableSSHError(exitError(t, "10"), "connection refused"))
}

func TestIsRetryableSSHError_ExitStatus127(t *testing.T) {
	assert.False(t, isRetryableSSHError(exitError(t, "127"), "connection refused"))
}

func TestIsRetryableSSHError_Nil(t *testing.T) {
	assert.False(t, isRetryableSSHError(nil, "connection refused"))
}

func TestIsRetryableSSHError_NonExitError(t *testing.T) {
	assert.False(t, isRetryableSSHError(fmt.Errorf("some other error"), "connection refused"))
}

func TestIsRetryableDockerError_RateLimit(t *testing.T) {
	stderr := `GET https://index.docker.io/v2/library/ubuntu/manifests/latest: ` +
		`TOOMANYREQUESTS: You have reached your unauthenticated pull rate limit.`
	assert.True(t, isRetryableDockerError(stderr))
}

func TestIsRetryableDockerError_Timeout(t *testing.T) {
	stderr := `Get "https://registry-1.docker.io/v2/": net/http: TLS handshake timeout`
	assert.True(t, isRetryableDockerError(stderr))
}

func TestIsRetryableDockerError_IOTimeout(t *testing.T) {
	stderr := `Get "https://registry-1.docker.io/v2/library/ubuntu/manifests/latest": i/o timeout`
	assert.True(t, isRetryableDockerError(stderr))
}

func TestIsRetryableDockerError_ConnectionReset(t *testing.T) {
	stderr := `error pulling image: read tcp 10.0.0.1:443: read: connection reset by peer`
	assert.True(t, isRetryableDockerError(stderr))
}

func TestIsRetryableDockerError_ServiceUnavailable(t *testing.T) {
	stderr := `received unexpected HTTP status: 503 Service Unavailable`
	assert.True(t, isRetryableDockerError(stderr))
}

func TestIsRetryableDockerError_RealFailure(t *testing.T) {
	stderr := `error resolving dockerfile: dockerfile not found`
	assert.False(t, isRetryableDockerError(stderr))
}

func TestIsRetryableDockerError_Empty(t *testing.T) {
	assert.False(t, isRetryableDockerError(""))
}

// transientExitErr is a deterministic, real *exec.ExitError (exit code 1) used
// by the retry-loop tests so the predicate's errors.As/exec.ExitCode checks
// exercise the same code path as real devsy CLI invocations.
func transientExitErr(t *testing.T) *exec.ExitError {
	return exitError(t, "1")
}

// withFastBackoffs temporarily replaces the package-level retry backoffs with
// millisecond-scale durations so the loop tests do not sleep for minutes. The
// real backoff values (30s/60s/120s for Docker, 5s/10s for SSH) are restored on
// test completion. Steps are preserved so exhaustion assertions stay meaningful.
func withFastBackoffs(t *testing.T) {
	t.Helper()
	origDocker := dockerPullBackoff
	origSSH := sshBackoff
	dockerPullBackoff = wait.Backoff{
		Steps:    origDocker.Steps,
		Duration: time.Millisecond,
		Factor:   1.0,
		Jitter:   0,
	}
	sshBackoff = wait.Backoff{
		Steps:    origSSH.Steps,
		Duration: time.Millisecond,
		Factor:   1.0,
		Jitter:   0,
	}
	t.Cleanup(func() {
		dockerPullBackoff = origDocker
		sshBackoff = origSSH
	})
}

// TestExecWithDockerRetry_SuccessFirstTry verifies a successful invocation is
// not retried and returns the captured stdout/stderr without error.
func TestExecWithDockerRetry_SuccessFirstTry(t *testing.T) {
	withFastBackoffs(t)
	calls := 0
	out, stderr, err := execWithDockerRetry(context.Background(),
		func(context.Context) (string, string, error) {
			calls++
			return "ok", "done", nil
		},
	)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Equal(t, "ok", out)
	assert.Equal(t, "done", stderr)
}

// TestExecWithDockerRetry_NonRetryableError verifies a non-transient failure
// short-circuits after a single attempt and wraps the error with the attempt
// count.
func TestExecWithDockerRetry_NonRetryableError(t *testing.T) {
	withFastBackoffs(t)
	calls := 0
	orig := fmt.Errorf("dockerfile not found")
	out, stderr, err := execWithDockerRetry(context.Background(),
		func(context.Context) (string, string, error) {
			calls++
			return "partial", "error resolving dockerfile: dockerfile not found", orig
		},
	)
	require.Error(t, err)
	assert.Equal(t, 1, calls)
	assert.Equal(t, "partial", out)
	assert.Equal(t, "error resolving dockerfile: dockerfile not found", stderr)
	assert.ErrorIs(t, err, orig)
	assert.Contains(t, err.Error(), "after 1 attempts")
}

// TestExecWithDockerRetry_RetryThenSuccess verifies a transient Docker error is
// retried and that a subsequent success returns the successful result.
func TestExecWithDockerRetry_RetryThenSuccess(t *testing.T) {
	withFastBackoffs(t)
	calls := 0
	out, stderr, err := execWithDockerRetry(context.Background(),
		func(context.Context) (string, string, error) {
			calls++
			if calls == 1 {
				return "", "TOOMANYREQUESTS: rate limit", fmt.Errorf("pull failed")
			}
			return "ok", "", nil
		},
	)
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
	assert.Equal(t, "ok", out)
	assert.Equal(t, "", stderr)
}

// TestExecWithDockerRetry_RetryExhausted verifies that a persistently
// transient error is retried up to dockerPullBackoff.Steps (4) times and the
// final error reports the full attempt count.
func TestExecWithDockerRetry_RetryExhausted(t *testing.T) {
	withFastBackoffs(t)
	calls := 0
	orig := fmt.Errorf("pull failed")
	_, _, err := execWithDockerRetry(context.Background(),
		func(context.Context) (string, string, error) {
			calls++
			return "", "i/o timeout", orig
		},
	)
	require.Error(t, err)
	assert.Equal(t, dockerPullBackoff.Steps, calls)
	assert.ErrorIs(t, err, orig)
	assert.Contains(t, err.Error(),
		fmt.Sprintf("after %d attempts", dockerPullBackoff.Steps))
}

// TestExecWithDockerRetry_ContextCanceled verifies that an already-canceled
// context surfaces context.Canceled without wrapping it in the attempt-count
// message.
func TestExecWithDockerRetry_ContextCanceled(t *testing.T) {
	withFastBackoffs(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	_, _, err := execWithDockerRetry(ctx,
		func(context.Context) (string, string, error) {
			calls++
			return "", "connection reset by peer", fmt.Errorf("pull failed")
		},
	)
	// Either the context blocked the call entirely (0 calls) or it was canceled
	// mid-retry; either way context.Canceled must propagate unwrapped.
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.NotContains(t, err.Error(), "after")
}

// TestExecWithSSHRetry_SuccessFirstTry verifies a successful SSH invocation is
// not retried.
func TestExecWithSSHRetry_SuccessFirstTry(t *testing.T) {
	withFastBackoffs(t)
	calls := 0
	out, err := execWithSSHRetry(context.Background(), "ws",
		func(context.Context) (string, string, error) {
			calls++
			return "ok", "", nil
		},
	)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Equal(t, "ok", out)
}

// TestExecWithSSHRetry_NonRetryableError verifies a non-transient remote
// command failure is not retried and is wrapped with the attempt count plus the
// captured stderr (the loop attaches stderr whenever it is non-empty, even for
// non-retryable failures, to aid debugging).
func TestExecWithSSHRetry_NonRetryableError(t *testing.T) {
	withFastBackoffs(t)
	calls := 0
	orig := transientExitErr(t)
	stderrMsg := "cat: /home/user/post-attach.out: No such file or directory"
	out, err := execWithSSHRetry(context.Background(), "ws",
		func(context.Context) (string, string, error) {
			calls++
			return "partial", stderrMsg, orig
		},
	)
	require.Error(t, err)
	assert.Equal(t, 1, calls)
	assert.Equal(t, "partial", out)
	assert.ErrorIs(t, err, orig)
	assert.Contains(t, err.Error(), "after 1 attempts")
	assert.Contains(t, err.Error(), "stderr:")
	assert.True(t, strings.Contains(err.Error(), stderrMsg))
}

// TestExecWithSSHRetry_RetryThenSuccess verifies a transient SSH error is
// retried and that a subsequent success returns the successful stdout.
func TestExecWithSSHRetry_RetryThenSuccess(t *testing.T) {
	withFastBackoffs(t)
	calls := 0
	out, err := execWithSSHRetry(context.Background(), "ws",
		func(context.Context) (string, string, error) {
			calls++
			if calls == 1 {
				return "", "ssh: connect to host 127.0.0.1 port 22: Connection refused", transientExitErr(
					t,
				)
			}
			return "ok", "", nil
		},
	)
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
	assert.Equal(t, "ok", out)
}

// TestExecWithSSHRetry_RetryExhausted verifies a persistently transient SSH
// error is retried up to sshBackoff.Steps (3) times and the final error
// includes both the attempt count and the captured stderr.
func TestExecWithSSHRetry_RetryExhausted(t *testing.T) {
	withFastBackoffs(t)
	calls := 0
	orig := transientExitErr(t)
	stderrMsg := "ssh: connect to host 10.0.0.1 port 22: Connection timed out"
	out, err := execWithSSHRetry(context.Background(), "ws",
		func(context.Context) (string, string, error) {
			calls++
			return "last", stderrMsg, orig
		},
	)
	require.Error(t, err)
	assert.Equal(t, sshBackoff.Steps, calls)
	assert.Equal(t, "last", out)
	assert.ErrorIs(t, err, orig)
	assert.Contains(t, err.Error(),
		fmt.Sprintf("after %d attempts", sshBackoff.Steps))
	assert.Contains(t, err.Error(), "stderr:")
	assert.True(t, strings.Contains(err.Error(), stderrMsg))
}

// TestExecWithSSHRetry_ContextDeadlineExceeded verifies that a deadline firing
// during a backoff wait surfaces context.DeadlineExceeded unwrapped. The
// per-step backoff duration is set longer than the context deadline so the
// deadline trips the wait between attempts rather than completing all steps.
func TestExecWithSSHRetry_ContextDeadlineExceeded(t *testing.T) {
	t.Helper()
	origSSH := sshBackoff
	sshBackoff = wait.Backoff{
		Steps:    origSSH.Steps,
		Duration: 200 * time.Millisecond,
		Factor:   1.0,
		Jitter:   0,
	}
	t.Cleanup(func() { sshBackoff = origSSH })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	t.Cleanup(cancel)
	_, err := execWithSSHRetry(ctx, "ws",
		func(context.Context) (string, string, error) {
			return "", "connection refused", transientExitErr(t)
		},
	)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotContains(t, err.Error(), "after")
}
