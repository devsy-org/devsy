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
	"container exec session produced no output",
	"ssh handshake made no progress",
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

type execFunc func(ctx context.Context) (stdout, stderr string, err error)

// retryPolicy describes one retry loop. label prefixes the retry log line and
// kind names the error class in the budget-exhausted message.
type retryPolicy struct {
	backoff   wait.Backoff
	retryable func(err error, stderr string) bool
	label     string
	kind      string
}

// retryResult is the outcome of runWithRetry. gaveUp is set when the loop
// stopped on a failed attempt, so callers add the attempt count to err.
type retryResult struct {
	stdout, stderr string
	attempts       int
	err            error
	gaveUp         bool
}

func runWithRetry(ctx context.Context, policy retryPolicy, fn execFunc) retryResult {
	var res retryResult
	for res.attempts = 1; res.attempts <= policy.backoff.Steps; res.attempts++ {
		if err := ctx.Err(); err != nil {
			res.err = err
			return res
		}
		res.stdout, res.stderr, res.err = fn(ctx)
		if res.err == nil {
			return res
		}
		if !policy.retryable(res.err, res.stderr) || res.attempts == policy.backoff.Steps {
			break
		}
		delay := nextBackoffDelay(policy.backoff, res.attempts)
		if !retryFitsBudget(ctx, delay) {
			res.err = fmt.Errorf(
				"after %d attempts: %s; retry not attempted because "+
					"remaining deadline budget was insufficient (next retry delay: %s): %w",
				res.attempts, policy.kind, delay, res.err,
			)
			return res
		}
		ginkgo.GinkgoWriter.Printf(
			"%s attempt %d failed with transient error, retrying after %s: %s\n",
			policy.label, res.attempts, delay, res.err,
		)
		if err := waitForRetry(ctx, delay); err != nil {
			res.err = err
			return res
		}
	}
	res.gaveUp = true
	return res
}

// execWithDockerRetry runs fn and retries if the failure looks transient:
// either a Docker registry error or a retryable SSH setup error in stderr
// (up commands build the tunnel before the workspace starts). Returns the
// last stdout, stderr, and error.
func execWithDockerRetry(ctx context.Context, fn execFunc) (string, string, error) {
	res := runWithRetry(ctx, retryPolicy{
		backoff: dockerPullBackoff,
		retryable: func(err error, stderr string) bool {
			return isRetryableDockerError(stderr) || isRetryableSSHError(err, stderr)
		},
		label: "[retry]",
		kind:  "retryable transient error",
	}, fn)
	if res.gaveUp {
		return res.stdout, res.stderr, fmt.Errorf("after %d attempts: %w", res.attempts, res.err)
	}
	return res.stdout, res.stderr, res.err
}

// execWithSSHRetry runs fn and retries if the error indicates a transient SSH
// connection failure. The fn callback must return stdout, stderr, and error so
// that stderr can be inspected for SSH-specific patterns.
func execWithSSHRetry(ctx context.Context, workspace string, fn execFunc) (string, error) {
	res := runWithRetry(ctx, retryPolicy{
		backoff:   sshBackoff,
		retryable: isRetryableSSHError,
		label:     "[retry] ssh " + workspace + ":",
		kind:      "retryable SSH error",
	}, fn)
	if !res.gaveUp || res.err == nil {
		return res.stdout, res.err
	}
	if res.stderr != "" {
		return res.stdout, fmt.Errorf(
			"after %d attempts: %w (stderr: %s)", res.attempts, res.err, res.stderr,
		)
	}
	return res.stdout, fmt.Errorf("after %d attempts: %w", res.attempts, res.err)
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

// RetryFixed calls fn up to attempts times, sleeping delay between failed
// attempts, and returns nil on the first success. When attempts are
// exhausted it returns the last error wrapped with the attempt count.
// Cancelling ctx stops retrying: the returned error wraps both ctx.Err() and
// the last fn error. attempts below 1 is treated as 1.
func RetryFixed(
	ctx context.Context,
	attempts int,
	delay time.Duration,
	fn func(ctx context.Context) error,
) error {
	for attempt := 1; ; attempt++ {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("after %d attempts: %w (last error: %w)", attempt, ctxErr, err)
		}
		if attempt >= attempts {
			return fmt.Errorf("after %d attempts: %w", attempt, err)
		}
		ginkgo.GinkgoWriter.Printf(
			"[retry] attempt %d/%d failed, retrying after %s: %v\n", attempt, attempts, delay, err,
		)
		if waitErr := waitForRetry(ctx, delay); waitErr != nil {
			return fmt.Errorf("after %d attempts: %w (last error: %w)", attempt, waitErr, err)
		}
	}
}
