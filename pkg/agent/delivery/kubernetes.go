package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"strings"
	"sync"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/pkg/agent"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/version"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	execerr "k8s.io/client-go/util/exec"
	"k8s.io/client-go/util/retry"
)

var _ AgentDelivery = (*KubernetesDelivery)(nil)

// ArgvExecFunc runs argv in a dev container with the given streams.
type ArgvExecFunc func(ctx context.Context, argv []string, streams driver.Streams) error

// KubernetesDelivery gets the agent binary into the pod over the cluster's
// exec API.
type KubernetesDelivery struct {
	Exec ArgvExecFunc

	// ExpectedVersion defaults to version.GetVersion() when empty.
	ExpectedVersion string

	// InstallPath overrides where the agent binary is installed inside the
	// container.
	InstallPath string

	// execStreamIdleTimeout overrides the idle timeout in tests.
	execStreamIdleTimeout time.Duration
}

const (
	noDownloadToolExitCode   = 127
	downloadTimeoutSeconds   = 25
	execStreamIdleTimeout    = 30 * time.Second
	execStreamMaxAttempts    = 2
	execStreamCleanupTimeout = 5 * time.Second
)

var errExecStreamIdleTimeout = errors.New("exec-stream delivery stalled")

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

	var downloadErr error
	if opts.PreferInContainerDownload {
		if err := d.deliverViaDownload(ctx, destPath, opts.DownloadURL, opts.Arch); err != nil {
			downloadErr = err
			log.Warnf(
				"in-container download unavailable, falling back to exec-stream delivery: %v",
				err,
			)
		} else {
			log.Debugf("delivered agent binary to pod via in-container download")
			return nil
		}
	}

	if err := d.deliverViaExecStream(ctx, destPath, opts); err != nil {
		return deliveryFailure(downloadErr, err)
	}

	log.Debugf("delivered agent binary to pod via kubernetes exec-stream")
	return nil
}

func deliveryFailure(downloadErr, streamErr error) error {
	streamErr = fmt.Errorf("exec-stream delivery failed: %w", streamErr)
	if downloadErr == nil {
		return streamErr
	}
	return errors.Join(
		fmt.Errorf("in-container download failed: %w", downloadErr),
		streamErr,
	)
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
			if err := ctx.Err(); err != nil {
				return &permanentDeliveryError{err}
			}
			attempt++
			binary, err := opts.BinarySource(ctx, opts.Arch)
			if err != nil {
				return &permanentDeliveryError{fmt.Errorf("acquire binary: %w", err)}
			}
			defer func() { _ = binary.Close() }()

			streamErr := d.execStreamOnce(ctx, destPath, binary)
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
	if perm, ok := errors.AsType[*permanentDeliveryError](err); ok {
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
	tempPath, err := transferTempPath(destPath)
	if err != nil {
		return &permanentDeliveryError{err}
	}

	attemptCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	progress := newProgressReader(binary)
	monitorDone := make(chan struct{})
	monitorStop := make(chan struct{})
	monitor := execStreamProgressMonitor{
		ctx:         attemptCtx,
		cancel:      cancel,
		progress:    progress,
		idleTimeout: d.idleTimeout(),
		stop:        monitorStop,
		done:        monitorDone,
	}
	go monitor.run()

	quotedTemp := shellescape.Quote(tempPath)
	stageScript := fmt.Sprintf(
		`set -e; d=$(dirname %s); mkdir -p "$d"; rm -f %s; cat > %s`,
		quotedTemp, quotedTemp, quotedTemp,
	)
	stageErr := d.Exec(
		attemptCtx,
		[]string{"sh", "-c", stageScript},
		driver.Streams{Stdin: progress},
	)
	close(monitorStop)
	<-monitorDone
	if cause := context.Cause(attemptCtx); cause != nil {
		stageErr = cause
	}
	if stageErr == nil {
		stageErr = progress.completionError()
	}
	if stageErr != nil {
		d.cleanupStagedBinary(ctx, tempPath)
		return stageErr
	}

	commitCtx, commitCancel := context.WithTimeout(ctx, d.idleTimeout())
	defer commitCancel()
	commitScript := validationCommitScript(tempPath, destPath, d.expectedVersion(), progress.size())
	var stderr bytes.Buffer
	if err := d.Exec(
		commitCtx,
		[]string{"sh", "-c", commitScript},
		driver.Streams{Stderr: &stderr},
	); err != nil {
		d.cleanupStagedBinary(ctx, tempPath)
		validationErr := fmt.Errorf(
			"validate staged agent: %w (%s)",
			err,
			strings.TrimSpace(stderr.String()),
		)
		if isTransientDeliveryError(err) {
			return validationErr
		}
		return &permanentDeliveryError{validationErr}
	}
	return nil
}

func (d *KubernetesDelivery) idleTimeout() time.Duration {
	if d.execStreamIdleTimeout > 0 {
		return d.execStreamIdleTimeout
	}
	return execStreamIdleTimeout
}

func transferTempPath(destPath string) (string, error) {
	cleanDest := path.Clean(destPath)
	if cleanDest == "." || cleanDest == "/" {
		return "", fmt.Errorf("invalid agent destination path %q", destPath)
	}
	return path.Join(path.Dir(cleanDest), ".devsy-transfer-"+uuid.NewString()), nil
}

func validationCommitScript(tempPath, destPath, expectedVersion string, expectedSize int64) string {
	quotedTemp := shellescape.Quote(tempPath)
	quotedDest := shellescape.Quote(destPath)
	quotedExpected := shellescape.Quote(expectedVersion)
	return fmt.Sprintf(`set -e
actual_size="$(wc -c < %s)"
[ "$actual_size" -eq %d ] || {
  rm -f %s
  echo "staged Devsy agent size mismatch" >&2
  exit 1
}
chmod 0755 %s
actual="$(%s --version 2>/dev/null)" || {
  rm -f %s
  echo "staged Devsy agent is not executable" >&2
  exit 1
}
if [ "$actual" != %s ]; then
  rm -f %s
  echo "staged Devsy agent version mismatch" >&2
  exit 1
fi
mv -f %s %s`,
		quotedTemp, expectedSize, quotedTemp, quotedTemp, quotedTemp,
		quotedTemp, quotedExpected, quotedTemp, quotedTemp, quotedDest,
	)
}

func (d *KubernetesDelivery) cleanupStagedBinary(parent context.Context, tempPath string) {
	cleanupCtx, cancel := context.WithTimeout(
		context.WithoutCancel(parent),
		execStreamCleanupTimeout,
	)
	defer cancel()
	if err := d.Exec(
		cleanupCtx,
		[]string{"sh", "-c", "rm -f " + shellescape.Quote(tempPath)},
		driver.Streams{},
	); err != nil {
		log.Debugf("failed to clean up staged agent %s: %v", tempPath, err)
	}
}

type progressReader struct {
	reader       io.Reader
	mu           sync.Mutex
	lastProgress time.Time
	bytesRead    int64
	readErr      error
}

func newProgressReader(reader io.Reader) *progressReader {
	return &progressReader{reader: reader, lastProgress: time.Now()}
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.mu.Lock()
	defer r.mu.Unlock()
	if n > 0 {
		r.lastProgress = time.Now()
		r.bytesRead += int64(n)
	}
	if err != nil {
		r.readErr = err
	}
	return n, err
}

func (r *progressReader) completionError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if errors.Is(r.readErr, io.EOF) && r.bytesRead > 0 {
		return nil
	}
	if r.readErr != nil && !errors.Is(r.readErr, io.EOF) {
		return &permanentDeliveryError{fmt.Errorf("read agent binary: %w", r.readErr)}
	}
	return fmt.Errorf("incomplete agent stream: %w", io.ErrUnexpectedEOF)
}

func (r *progressReader) size() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bytesRead
}

func (r *progressReader) idleFor(now time.Time) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return now.Sub(r.lastProgress)
}

type execStreamProgressMonitor struct {
	ctx         context.Context
	cancel      context.CancelCauseFunc
	progress    *progressReader
	idleTimeout time.Duration
	stop        <-chan struct{}
	done        chan<- struct{}
}

func (m *execStreamProgressMonitor) run() {
	defer close(m.done)
	ticker := time.NewTicker(max(m.idleTimeout/2, time.Nanosecond))
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.stop:
			return
		case now := <-ticker.C:
			if m.progress.idleFor(now) >= m.idleTimeout {
				m.cancel(errExecStreamIdleTimeout)
				return
			}
		}
	}
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
	if errors.Is(err, errExecStreamIdleTimeout) {
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

// PodExecFunc is retained for callers using the original pod-specific name.
type PodExecFunc = ArgvExecFunc
