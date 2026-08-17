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

func TestIsRetryableSSHError_NoSuchContainer(t *testing.T) {
	assert.True(
		t,
		isRetryableSSHError(
			exitError(t, "1"),
			"Error: no such container: devsy-workspace-abc123",
		),
	)
}

func TestIsRetryableSSHError_BrokenPipe(t *testing.T) {
	assert.True(
		t,
		isRetryableSSHError(
			exitError(t, "1"),
			"write: broken pipe",
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

func transientExitErr(t *testing.T) *exec.ExitError {
	return exitError(t, "1")
}

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
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.NotContains(t, err.Error(), "after")
}

func TestExecWithDockerRetry_ContextDeadlineExceeded(t *testing.T) {
	t.Helper()
	origDocker := dockerPullBackoff
	dockerPullBackoff = wait.Backoff{
		Steps:    origDocker.Steps,
		Duration: 200 * time.Millisecond,
		Factor:   1.0,
		Jitter:   0,
	}
	t.Cleanup(func() { dockerPullBackoff = origDocker })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	t.Cleanup(cancel)
	_, _, err := execWithDockerRetry(ctx,
		func(context.Context) (string, string, error) {
			return "", "i/o timeout", fmt.Errorf("pull failed")
		},
	)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotContains(t, err.Error(), "after")
}

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
