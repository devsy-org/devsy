package framework

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"k8s.io/apimachinery/pkg/util/wait"
)

// dockerPullBackoff defines retry timing for transient Docker registry errors.
// 4 total attempts (1 initial + 3 retries) with waits of ~30s, ~60s, ~120s.
var dockerPullBackoff = wait.Backoff{
	Steps:    4,
	Duration: 30 * time.Second,
	Factor:   2.0,
	Jitter:   0.1,
}

// sshBackoff defines retry timing for transient SSH failures on Windows+WSL
// runners where the devsy agent binary injection can intermittently fail.
// 3 total attempts (1 initial + 2 retries) with waits of ~5s, ~10s.
var sshBackoff = wait.Backoff{
	Steps:    3,
	Duration: 5 * time.Second,
	Factor:   2.0,
	Jitter:   0.1,
}

// retryableDockerPatterns are stderr substrings indicating a transient Docker
// registry error that is worth retrying.
var retryableDockerPatterns = []string{
	"TOOMANYREQUESTS",
	"rate limit",
	"TLS handshake timeout",
	"i/o timeout",
	"connection reset by peer",
	"503 Service Unavailable",
}

// retryableSSHPatterns are stderr substrings indicating a transient SSH
// connection error (as opposed to a remote command that legitimately failed).
var retryableSSHPatterns = []string{
	"connection refused",
	"connection reset",
	"ssh: connect to host",
	"no such container",
	"connection timed out",
	"broken pipe",
}

// isRetryableSSHError returns true when the error indicates a transient SSH
// connection failure. Both exit code 1 AND an SSH-specific pattern in stderr
// are required so that remote command failures (e.g. cat on a missing file)
// are not mistakenly retried.
func isRetryableSSHError(err error, stderr string) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	if exitErr.ExitCode() != 1 {
		return false
	}
	lower := strings.ToLower(stderr)
	for _, pattern := range retryableSSHPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// isRetryableDockerError returns true if stderr contains a transient Docker
// registry error (rate limits, timeouts, connection resets).
func isRetryableDockerError(stderr string) bool {
	lower := strings.ToLower(stderr)
	for _, pattern := range retryableDockerPatterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

// execWithDockerRetry runs fn and retries if stderr indicates a transient
// Docker registry error. Returns the last stdout, stderr, and error.
func execWithDockerRetry(
	ctx context.Context,
	fn func(ctx context.Context) (stdout, stderr string, err error),
) (string, string, error) {
	var lastStdout, lastStderr string
	var lastErr error
	attempt := 0

	err := wait.ExponentialBackoffWithContext(ctx, dockerPullBackoff,
		func(ctx context.Context) (bool, error) {
			attempt++
			lastStdout, lastStderr, lastErr = fn(ctx)
			if lastErr == nil {
				return true, nil // success
			}
			if isRetryableDockerError(lastStderr) {
				ginkgo.GinkgoWriter.Printf(
					"[retry] attempt %d failed with transient Docker error, retrying: %s\n",
					attempt, lastErr,
				)
				return false, nil // retry
			}
			return false, lastErr // non-retryable, stop immediately
		},
	)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return lastStdout, lastStderr, err
	}
	if err != nil && lastErr != nil {
		return lastStdout, lastStderr, fmt.Errorf("after %d attempts: %w", attempt, lastErr)
	}
	return lastStdout, lastStderr, err
}

// execWithSSHRetry runs fn and retries if the error indicates a transient SSH
// connection failure. The fn callback must return stdout, stderr, and error so
// that stderr can be inspected for SSH-specific patterns.
func execWithSSHRetry(
	ctx context.Context,
	workspace string,
	fn func(ctx context.Context) (stdout, stderr string, err error),
) (string, error) {
	var lastOut string
	var lastStderr string
	var lastErr error
	attempt := 0

	err := wait.ExponentialBackoffWithContext(ctx, sshBackoff,
		func(ctx context.Context) (bool, error) {
			attempt++
			lastOut, lastStderr, lastErr = fn(ctx)
			if lastErr == nil {
				return true, nil // success
			}
			if isRetryableSSHError(lastErr, lastStderr) {
				ginkgo.GinkgoWriter.Printf(
					"[retry] ssh %s: attempt %d failed with transient error, retrying: %s\n",
					workspace, attempt, lastErr,
				)
				return false, nil // retry
			}
			return false, lastErr // non-retryable, stop immediately
		},
	)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return lastOut, err
	}
	if err != nil && lastErr != nil {
		return lastOut, fmt.Errorf("after %d attempts: %w", attempt, lastErr)
	}
	return lastOut, err
}
