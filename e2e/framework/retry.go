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
// The waits are deliberately short so retries consume only a minority of a
// short spec's deadline.
var dockerPullBackoff = wait.Backoff{
	Steps:    4,
	Duration: 5 * time.Second,
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
	"workspace not found",
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
	if strings.Contains(lower, "fork/exec") && strings.Contains(lower, "permission denied") {
		return true
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

	for attempt = 1; attempt <= dockerPullBackoff.Steps; attempt++ {
		if err := ctx.Err(); err != nil {
			return lastStdout, lastStderr, err
		}
		lastStdout, lastStderr, lastErr = fn(ctx)
		if lastErr == nil {
			return lastStdout, lastStderr, nil
		}
		if !isRetryableDockerError(lastStderr) || attempt == dockerPullBackoff.Steps {
			break
		}
		delay := nextBackoffDelay(dockerPullBackoff, attempt)
		if !retryFitsBudget(ctx, delay) {
			return lastStdout, lastStderr, fmt.Errorf(
				"after %d attempts: retryable Docker error; retry not attempted because "+
					"remaining deadline budget was insufficient (next retry delay: %s): %w",
				attempt, delay, lastErr,
			)
		}
		ginkgo.GinkgoWriter.Printf(
			"[retry] attempt %d failed with transient Docker error, retrying after %s: %s\n",
			attempt, delay, lastErr,
		)
		if err := waitForRetry(ctx, delay); err != nil {
			return lastStdout, lastStderr, err
		}
	}
	return lastStdout, lastStderr, fmt.Errorf("after %d attempts: %w", attempt, lastErr)
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

	for attempt = 1; attempt <= sshBackoff.Steps; attempt++ {
		if err := ctx.Err(); err != nil {
			return lastOut, err
		}
		lastOut, lastStderr, lastErr = fn(ctx)
		if lastErr == nil {
			return lastOut, nil
		}
		if !isRetryableSSHError(lastErr, lastStderr) || attempt == sshBackoff.Steps {
			break
		}
		delay := nextBackoffDelay(sshBackoff, attempt)
		if !retryFitsBudget(ctx, delay) {
			return lastOut, fmt.Errorf(
				"after %d attempts: retryable SSH error; retry not attempted because "+
					"remaining deadline budget was insufficient (next retry delay: %s): %w",
				attempt, delay, lastErr,
			)
		}
		ginkgo.GinkgoWriter.Printf(
			"[retry] ssh %s: attempt %d failed with transient error, retrying after %s: %s\n",
			workspace, attempt, delay, lastErr,
		)
		if err := waitForRetry(ctx, delay); err != nil {
			return lastOut, err
		}
	}
	if lastErr != nil {
		if lastStderr != "" {
			return lastOut, fmt.Errorf(
				"after %d attempts: %w (stderr: %s)", attempt, lastErr, lastStderr,
			)
		}
		return lastOut, fmt.Errorf("after %d attempts: %w", attempt, lastErr)
	}
	return lastOut, lastErr
}

func retryFitsBudget(ctx context.Context, delay time.Duration) bool {
	deadline, ok := ctx.Deadline()
	return !ok || time.Until(deadline) > delay
}

func nextBackoffDelay(backoff wait.Backoff, retryNumber int) time.Duration {
	delay := backoff.DelayFunc()
	var next time.Duration
	for range retryNumber {
		next = delay()
	}
	return next
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
