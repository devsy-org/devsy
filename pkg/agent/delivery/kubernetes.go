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
	"k8s.io/apimachinery/pkg/util/wait"
	execerr "k8s.io/client-go/util/exec"
	"k8s.io/client-go/util/retry"
)

var _ AgentDelivery = (*KubernetesDelivery)(nil)

// PodExecFunc runs argv in the workspace pod's dev container with the given streams.
type PodExecFunc func(ctx context.Context, argv []string, streams driver.Streams) error

// KubernetesDelivery gets the agent binary into the pod over the cluster's
// exec API.
type KubernetesDelivery struct {
	Exec PodExecFunc

	// ExpectedVersion defaults to version.GetVersion() when empty.
	ExpectedVersion string

	// InstallPath overrides where the agent binary is installed inside the
	// container.
	InstallPath string
}

const (
	noDownloadToolExitCode   = 127
	downloadTimeoutSeconds   = 25
	execStreamAttemptTimeout = 30 * time.Second
	execStreamMaxAttempts    = 2
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

	destPath := d.destPath()

	// Skip delivery when the in-pod binary already matches.
	expected := d.expectedVersion()
	if actual := d.detectVersion(ctx, destPath); actual != "" && actual == expected {
		log.Debugf("remote agent version matches expected version %s, skipping delivery", expected)
		return nil
	}

	if opts.PreferInContainerDownload {
		if err := d.deliverViaDownload(ctx, destPath, opts.DownloadURL, opts.Arch); err != nil {
			log.Debugf(
				"in-container download unavailable, falling back to exec-stream delivery: %v",
				err,
			)
		} else {
			log.Debugf("delivered agent binary to pod via in-container download")
			return nil
		}
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

	var stderr bytes.Buffer
	if err := d.Exec(
		ctx,
		[]string{"sh", "-c", script},
		driver.Streams{Stderr: &stderr},
	); err != nil {
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

func (d *KubernetesDelivery) deliverViaExecStream(
	ctx context.Context,
	destPath string,
	opts PostStartOptions,
) error {
	attempt := 0
	err := retry.OnError(
		wait.Backoff{Steps: execStreamMaxAttempts},
		isTransientDeliveryError,
		func() error {
			attempt++
			binary, err := opts.BinarySource(ctx, opts.Arch)
			if err != nil {
				return &permanentDeliveryError{fmt.Errorf("acquire binary: %w", err)}
			}
			defer func() { _ = binary.Close() }()

			attemptCtx, cancel := context.WithTimeout(ctx, execStreamAttemptTimeout)
			defer cancel()
			streamErr := d.execStreamOnce(attemptCtx, destPath, binary)
			if streamErr != nil && isTransientDeliveryError(streamErr) &&
				attempt < execStreamMaxAttempts {
				log.Warnf(
					"exec-stream delivery attempt %d/%d stalled or reset, retrying: %v",
					attempt, execStreamMaxAttempts, streamErr,
				)
			}
			return streamErr
		},
	)
	var perm *permanentDeliveryError
	if errors.As(err, &perm) {
		return perm.err
	}
	return err
}

type permanentDeliveryError struct{ err error }

func (e *permanentDeliveryError) Error() string { return e.err.Error() }

func (e *permanentDeliveryError) Unwrap() error { return e.err }

func (d *KubernetesDelivery) execStreamOnce(
	ctx context.Context,
	destPath string,
	binary io.Reader,
) error {
	quotedDest := shellescape.Quote(destPath)
	script := fmt.Sprintf(
		`set -e; d=$(dirname %s); mkdir -p "$d"; `+
			`t=$(mktemp %s.XXXXXX); `+
			`cat > "$t" && chmod 0755 "$t" && mv -f "$t" %s || { rm -f "$t"; exit 1; }`,
		quotedDest, quotedDest, quotedDest,
	)
	return d.Exec(ctx, []string{"sh", "-c", script}, driver.Streams{Stdin: binary})
}

func isTransientDeliveryError(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := errors.AsType[*permanentDeliveryError](err); ok {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if _, ok := errors.AsType[net.Error](err); ok {
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

func (d *KubernetesDelivery) destPath() string {
	if d.InstallPath != "" {
		return d.InstallPath
	}
	return pkgconfig.ContainerDevsyHelperLocation
}

// detectVersion returns the agent version in the pod, or "" if absent or unprobeable.
func (d *KubernetesDelivery) detectVersion(ctx context.Context, destPath string) string {
	quotedPath := shellescape.Quote(destPath)
	script := fmt.Sprintf(`[ -x %s ] && %s --version 2>/dev/null || true`, quotedPath, quotedPath)

	var stdout bytes.Buffer
	err := d.Exec(ctx, []string{"sh", "-c", script}, driver.Streams{Stdout: &stdout})
	if err != nil {
		log.Debugf("failed to detect agent version in pod: %v", err)
		return ""
	}
	return strings.TrimSpace(stdout.String())
}
