package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/pkg/agent"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/version"
	execerr "k8s.io/client-go/util/exec"
)

var _ AgentDelivery = (*KubernetesDelivery)(nil)

// PodExecFunc runs argv in the workspace pod's dev container with the given streams.
type PodExecFunc func(ctx context.Context, argv []string, streams driver.Streams) error

// KubernetesDelivery gets the agent binary into the pod over the cluster's
// exec API. It prefers having the pod download its own binary (a short,
// no-stdin exec call) over streaming the binary's bytes through exec-stdin:
// a multi-hundred-MB write over that transport has been observed to hang
// indefinitely, with no error, until an OS-level TCP timeout eventually fires
// (tens of seconds), whereas a small command-only exec call is reliable.
// Exec-stdin streaming remains as a fallback for clusters without pod egress
// to a download URL.
type KubernetesDelivery struct {
	Exec PodExecFunc

	// ExpectedVersion defaults to version.GetVersion() when empty.
	ExpectedVersion string
}

const (
	// noDownloadToolExitCode is returned by the in-container download script
	// when the image has neither curl nor wget; this is a permanent failure
	// (retrying can't add a binary to the image), so it's not retried and
	// isn't logged as a real error -- it just means falling back to exec-stream.
	noDownloadToolExitCode = 127

	// downloadTimeoutSeconds bounds the in-container curl/wget call so a
	// cluster with no egress to the download URL fails fast instead of
	// hanging for the exec call's full lifetime.
	downloadTimeoutSeconds = 25

	// execStreamAttemptTimeout bounds a single exec-stdin delivery attempt so
	// a stalled stream is detected and retried in seconds, not by waiting on
	// an OS-level TCP timeout.
	execStreamAttemptTimeout = 30 * time.Second

	// execStreamMaxAttempts retries the exec-stdin fallback only for errors
	// classified as transient (see isTransientDeliveryError); a permanent
	// failure returns immediately without paying this cost twice.
	execStreamMaxAttempts = 2
)

func (d *KubernetesDelivery) Phase() DeliveryPhase {
	return PhasePostStart
}

func (d *KubernetesDelivery) DeliverPreStart(_ context.Context, _ PreStartOptions) error {
	return fmt.Errorf("KubernetesDelivery does not support pre-start delivery")
}

func (d *KubernetesDelivery) DeliverPostStart(ctx context.Context, opts PostStartOptions) error {
	if opts.BinarySource == nil {
		return fmt.Errorf("binary source is required for kubernetes delivery")
	}
	if d.Exec == nil {
		return fmt.Errorf("exec function is required for kubernetes delivery")
	}

	destPath := pkgconfig.ContainerDevsyHelperLocation

	// Skip delivery when the in-pod binary already matches.
	expected := d.expectedVersion()
	if actual := d.detectVersion(ctx, destPath); actual != "" && actual == expected {
		log.Debugf("remote agent version matches expected version %s, skipping delivery", expected)
		return nil
	}

	if err := d.deliverViaDownload(ctx, destPath, opts.DownloadURL, opts.Arch); err != nil {
		log.Debugf(
			"in-container download unavailable, falling back to exec-stream delivery: %v",
			err,
		)
	} else {
		log.Debugf("delivered agent binary to pod via in-container download")
		return nil
	}

	if err := d.deliverViaExecStream(ctx, destPath, opts); err != nil {
		return fmt.Errorf("write binary to container: %w", err)
	}

	log.Debugf("delivered agent binary to pod via kubernetes exec-stream")
	return nil
}

func (d *KubernetesDelivery) Cleanup(_ context.Context, _ string) error {
	return nil
}

// deliverViaDownload has the pod fetch its own agent binary via curl/wget
// instead of streaming its bytes through exec-stdin. Returns an error
// (never retried here) when no download URL is configured, the URL can't be
// built, or the image has neither curl nor wget; the caller falls back to
// exec-stream delivery in every case.
func (d *KubernetesDelivery) deliverViaDownload(
	ctx context.Context,
	destPath, downloadURL, arch string,
) error {
	if downloadURL == "" {
		return fmt.Errorf("no download URL configured")
	}

	fetchURL, err := agent.AgentDownloadURL(downloadURL, arch)
	if err != nil {
		return fmt.Errorf("build download URL: %w", err)
	}

	script := downloadScript(destPath, fetchURL)

	// The kubernetes exec API rejects a request with none of stdin/stdout/
	// stderr set; capture stderr for diagnostics even though delivery itself
	// needs no output.
	var stderr bytes.Buffer
	if err := d.Exec(ctx, []string{"sh", "-c", script}, driver.Streams{Stderr: &stderr}); err != nil {
		var codeErr execerr.CodeExitError
		if errors.As(err, &codeErr) && codeErr.Code == noDownloadToolExitCode {
			return fmt.Errorf("no curl or wget in the image: %w", err)
		}
		return fmt.Errorf("download in container: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func downloadScript(destPath, downloadURL string) string {
	quotedDest := shellescape.Quote(destPath)
	quotedURL := shellescape.Quote(downloadURL)
	return fmt.Sprintf(
		`set -e
d=$(dirname %s); mkdir -p "$d"
t=$(mktemp %s.XXXXXX)
trap 'rm -f "$t"' EXIT
if command -v curl >/dev/null 2>&1; then
  curl -fsSL --max-time %d %s -o "$t"
elif command -v wget >/dev/null 2>&1; then
  wget -q -T %d %s -O "$t"
else
  exit %d
fi
chmod 0755 "$t"
mv -f "$t" %s
`,
		quotedDest, quotedDest,
		downloadTimeoutSeconds, quotedURL,
		downloadTimeoutSeconds, quotedURL,
		noDownloadToolExitCode,
		quotedDest,
	)
}

// deliverViaExecStream streams the agent binary's bytes over exec-stdin, the
// fallback for clusters without pod egress to a download URL. Each attempt is
// bounded by execStreamAttemptTimeout, and only errors classified as
// transient are retried -- a permanent failure fails immediately rather than
// paying the same cost twice for an operation that can't succeed.
func (d *KubernetesDelivery) deliverViaExecStream(
	ctx context.Context,
	destPath string,
	opts PostStartOptions,
) error {
	var lastErr error
	for attempt := 1; attempt <= execStreamMaxAttempts; attempt++ {
		binary, err := opts.BinarySource(ctx, opts.Arch)
		if err != nil {
			return fmt.Errorf("acquire binary: %w", err)
		}

		attemptCtx, cancel := context.WithTimeout(ctx, execStreamAttemptTimeout)
		lastErr = d.execStreamOnce(attemptCtx, destPath, binary)
		cancel()
		_ = binary.Close()

		if lastErr == nil {
			return nil
		}
		if !isTransientDeliveryError(lastErr) {
			return lastErr
		}
		log.Warnf(
			"exec-stream delivery attempt %d/%d stalled or reset, retrying: %v",
			attempt, execStreamMaxAttempts, lastErr,
		)
	}
	return lastErr
}

// execStreamOnce writes to a temp file and atomically moves it into place so
// a failed stream never leaves an executable stub.
func (d *KubernetesDelivery) execStreamOnce(
	ctx context.Context,
	destPath string,
	binary io.Reader,
) error {
	script := fmt.Sprintf(
		`set -e; d=$(dirname %s); mkdir -p "$d"; `+
			`t=$(mktemp %s.XXXXXX); `+
			`cat > "$t" && chmod 0755 "$t" && mv -f "$t" %s || { rm -f "$t"; exit 1; }`,
		destPath, destPath, destPath,
	)
	return d.Exec(ctx, []string{"sh", "-c", script}, driver.Streams{Stdin: binary})
}

// isTransientDeliveryError reports whether err plausibly self-heals on
// retry: a stall, timeout, or reset on the underlying connection, including
// our own attempt deadline firing. A command that ran and failed on its own
// terms (a real exit code, a missing shell) is not transient.
func isTransientDeliveryError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	msg := err.Error()
	for _, marker := range []string{"i/o timeout", "broken pipe", "connection reset", "unexpected EOF"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func (d *KubernetesDelivery) expectedVersion() string {
	if d.ExpectedVersion != "" {
		return d.ExpectedVersion
	}
	return version.GetVersion()
}

// detectVersion returns the agent version in the pod, or "" if absent or unprobeable.
func (d *KubernetesDelivery) detectVersion(ctx context.Context, destPath string) string {
	script := fmt.Sprintf(`[ -x "%s" ] && "%s" --version 2>/dev/null || true`, destPath, destPath)

	var stdout bytes.Buffer
	err := d.Exec(ctx, []string{"sh", "-c", script}, driver.Streams{Stdout: &stdout})
	if err != nil {
		log.Debugf("failed to detect agent version in pod: %v", err)
		return ""
	}
	return strings.TrimSpace(stdout.String())
}
