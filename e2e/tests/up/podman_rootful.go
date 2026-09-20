package up

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
)

const (
	podmanHealthCheckTimeout = 20 * time.Second
	podmanRecoveryTimeout    = 90 * time.Second
	// podmanDiagCommandTimeout keeps a wedged daemon from stalling diagnostic
	// collection itself.
	podmanDiagCommandTimeout = 10 * time.Second
	// podmanDiagMaxOutput caps each diagnostic section so CI logs stay readable.
	podmanDiagMaxOutput = 8 * 1024
	// podmanBinName is the fallback binary when the rootful wrapper is absent.
	podmanBinName            = "podman"
	podmanRootfulWrapperName = "podman-rootful"
)

type podmanHealthClass int

const (
	podmanHealthOK podmanHealthClass = iota
	// podmanHealthTimeout: the daemon accepts connections but does not answer
	// (wedged).
	podmanHealthTimeout
	// podmanHealthUnavailable: the API socket or service is missing or
	// refusing connections.
	podmanHealthUnavailable
	// podmanHealthError: the daemon answered with an error, which points at
	// product or configuration state rather than a wedged service.
	podmanHealthError
)

func (c podmanHealthClass) String() string {
	switch c {
	case podmanHealthOK:
		return "ok"
	case podmanHealthTimeout:
		return "timeout"
	case podmanHealthUnavailable:
		return "unavailable"
	case podmanHealthError:
		return "error"
	}
	return "unknown"
}

// classifyPodmanHealthFailure buckets a failed probe so the caller can choose
// between infrastructure recovery and surfacing a real failure.
func classifyPodmanHealthFailure(healthCtx context.Context, output string) podmanHealthClass {
	if errors.Is(healthCtx.Err(), context.DeadlineExceeded) {
		return podmanHealthTimeout
	}
	lower := strings.ToLower(output)
	for _, pattern := range []string{
		"cannot connect",
		"connection refused",
		"no such file or directory",
	} {
		if strings.Contains(lower, pattern) {
			return podmanHealthUnavailable
		}
	}
	return podmanHealthError
}

// shouldAttemptPodmanRecovery reports whether a restart can help: recovery
// fixes a wedged or missing daemon, while restarting a responsive but
// erroring daemon would hide a product or configuration problem.
func shouldAttemptPodmanRecovery(class podmanHealthClass) bool {
	return class == podmanHealthTimeout || class == podmanHealthUnavailable
}

// podmanDaemonGate records the first unrecoverable daemon failure so later
// specs in the shard skip instead of cascading into identical infrastructure
// failures that would bury the actionable one. Scoped to the test process,
// which runs exactly one shard in CI.
type podmanDaemonGate struct {
	mu             sync.Mutex
	unhealthySince string
}

var rootfulDaemonGate = &podmanDaemonGate{}

func (g *podmanDaemonGate) markUnhealthy(spec string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.unhealthySince == "" {
		g.unhealthySince = spec
	}
}

func (g *podmanDaemonGate) unhealthy() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.unhealthySince
}

func checkPodmanHealth(ctx context.Context, wrapperPath string) (podmanHealthClass, error) {
	healthCtx, cancel := context.WithTimeout(ctx, podmanHealthCheckTimeout)
	defer cancel()

	cmd := exec.CommandContext( //nolint:gosec // G204: test-controlled path
		healthCtx,
		wrapperPath,
		"ps",
	)
	docker.PrepareForGroupCancellation(cmd)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return podmanHealthOK, nil
	}
	class := classifyPodmanHealthFailure(healthCtx, string(out))
	return class, fmt.Errorf(
		"rootful Podman readiness check failed (class: %s) or exceeded %s\n"+
			"command: %s ps\nDOCKER_HOST: %s\ncontext err: %v\noutput:\n%s\nerror: %w",
		class,
		podmanHealthCheckTimeout,
		wrapperPath,
		os.Getenv("DOCKER_HOST"),
		healthCtx.Err(),
		string(out),
		err,
	)
}

// runDiagCommand never fails the caller: diagnostics are best-effort so a
// broken host tool cannot hide the failure they are meant to explain.
func runDiagCommand(name string, args ...string) string {
	diagCtx, cancel := context.WithTimeout(context.Background(), podmanDiagCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(
		diagCtx,
		name,
		args...) //nolint:gosec // G204: fixed diagnostic commands
	docker.PrepareForGroupCancellation(cmd)
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil {
		text += fmt.Sprintf("\n(command failed: %v; context err: %v)", err, diagCtx.Err())
	}
	if len(text) > podmanDiagMaxOutput {
		text = text[:podmanDiagMaxOutput] + "\n... (truncated)"
	}
	return text
}

// collectPodmanDiagnostics prints bounded daemon state so the first failure
// in a shard carries the evidence needed to debug a wedge without a rerun.
func collectPodmanDiagnostics(wrapperPath string) {
	ginkgo.GinkgoWriter.Printf(
		"[podman-diagnostics] collecting bounded daemon state (each command capped at %s)\n",
		podmanDiagCommandTimeout,
	)
	sections := []struct {
		title string
		name  string
		args  []string
	}{
		{"podman version", wrapperPath, []string{"version"}},
		{"podman info", wrapperPath, []string{"info"}},
		{"podman ps -a", wrapperPath, []string{"ps", "-a"}},
		{
			"systemctl status podman.socket podman.service",
			"sudo",
			[]string{
				"systemctl", "status", "podman.socket", "podman.service", "--no-pager", "-l",
			},
		},
		{
			"journalctl podman units (last 100 lines)",
			"sudo",
			[]string{
				"journalctl", "-u", "podman.socket", "-u", "podman.service",
				"-n", "100", "--no-pager",
			},
		},
		{
			"podman-related processes",
			"sh",
			[]string{
				"-c",
				"ps -eo pid,ppid,stat,etime,cmd | grep -E 'podman|crun|conmon' | grep -v grep || true",
			},
		},
		{"disk usage", "df", []string{"-h", "/", "/var/lib/containers"}},
		{"memory", "free", []string{"-m"}},
	}
	for _, section := range sections {
		ginkgo.GinkgoWriter.Printf(
			"[podman-diagnostics] --- %s ---\n%s\n",
			section.title,
			runDiagCommand(section.name, section.args...),
		)
	}
}

// attemptPodmanRecovery performs the shard's one bounded restart of the
// rootful Podman socket and service, then re-probes health.
func attemptPodmanRecovery(ctx context.Context, wrapperPath string) error {
	ginkgo.GinkgoWriter.Println(
		"[podman-recovery] attempting single bounded restart of podman.socket and podman.service",
	)
	restartCtx, cancel := context.WithTimeout(ctx, podmanRecoveryTimeout)
	defer cancel()
	cmd := exec.CommandContext( //nolint:gosec // G204: fixed recovery command
		restartCtx,
		"sudo",
		"systemctl",
		"restart",
		"podman.socket",
		"podman.service",
	)
	docker.PrepareForGroupCancellation(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl restart failed: %w\noutput:\n%s", err, string(out))
	}
	class, err := checkPodmanHealth(ctx, wrapperPath)
	if err != nil {
		return fmt.Errorf("daemon still unhealthy after restart (class: %s): %w", class, err)
	}
	return nil
}

// setupRootfulPodman prepares the rootful Podman wrapper and docker provider
// and gates the shard on daemon health: once recovery has failed, remaining
// specs skip instead of re-failing on the same wedged infrastructure.
func setupRootfulPodman(ctx context.Context, initialDir string) *framework.Framework {
	wrapperPath := initialDir + "/bin/" + podmanRootfulWrapperName

	if since := rootfulDaemonGate.unhealthy(); since != "" {
		ginkgo.Skip(fmt.Sprintf(
			"rootful Podman daemon unhealthy since first failure in %q; "+
				"skipping to avoid cascading infrastructure failures",
			since,
		))
	}

	wrapper, err := os.Create(wrapperPath) //nolint:gosec // G304: test-controlled path
	framework.ExpectNoError(err)

	_, err = wrapper.WriteString("#!/bin/sh\nsudo podman \"$@\"\n")
	if err != nil {
		_ = wrapper.Close()
	}
	framework.ExpectNoError(err)
	framework.ExpectNoError(wrapper.Close())

	// #nosec G302 -- wrapper script needs execute permission
	framework.ExpectNoError(os.Chmod(wrapperPath, 0o755))
	ginkgo.DeferCleanup(func() {
		_ = os.Remove(wrapperPath)
	})

	class, healthErr := checkPodmanHealth(ctx, wrapperPath)
	if healthErr != nil {
		ginkgo.GinkgoWriter.Printf("[podman-health] readiness check failed: %v\n", healthErr)
		collectPodmanDiagnostics(wrapperPath)
		if shouldAttemptPodmanRecovery(class) {
			if recErr := attemptPodmanRecovery(ctx, wrapperPath); recErr != nil {
				rootfulDaemonGate.markUnhealthy(ginkgo.CurrentSpecReport().FullText())
				collectPodmanDiagnostics(wrapperPath)
				framework.ExpectNoError(fmt.Errorf(
					"rootful Podman daemon unhealthy and single recovery attempt failed: %w "+
						"(initial check: %v)",
					recErr,
					healthErr,
				))
			}
			ginkgo.GinkgoWriter.Println(
				"[podman-recovery] daemon healthy again after single restart",
			)
		} else {
			// The daemon answers but errors: a restart would only hide a
			// product or configuration problem, so fail without gating.
			framework.ExpectNoError(healthErr)
		}
	}

	f, err := setupDockerProvider(initialDir+"/bin", wrapperPath)
	framework.ExpectNoError(err)
	return f
}

type podmanCleanupDirs struct {
	initialDir string
	tempDir    string
}

// recoverPodmanCleanup reports bounded diagnostics for a failed workspace
// cleanup and, on a wedged or missing daemon, performs one restart plus one
// cleanup retry. A responsive but erroring daemon is left untouched:
// retrying there would hide product bugs. It returns the error the cleanup
// should report.
func recoverPodmanCleanup(
	ctx context.Context,
	f *framework.Framework,
	dirs podmanCleanupDirs,
	cleanupErr error,
) error {
	wrapperPath := dirs.initialDir + "/bin/" + podmanRootfulWrapperName
	if _, err := os.Stat(wrapperPath); err != nil {
		// Not a rootful shard: keep the previous minimal diagnostic.
		ginkgo.GinkgoWriter.Printf(
			"cleanup failure podman ps -a:\n%s\n",
			runDiagCommand(podmanBinName, "ps", "-a"),
		)
		return cleanupErr
	}

	collectPodmanDiagnostics(wrapperPath)

	// The cleanup context may already be canceled after a spec timeout; use a
	// detached context so classification and recovery stay deterministic,
	// mirroring CleanupWorkspace.
	recoveryCtx := context.WithoutCancel(ctx)
	class, healthErr := checkPodmanHealth(recoveryCtx, wrapperPath)
	if healthErr == nil || !shouldAttemptPodmanRecovery(class) {
		return cleanupErr
	}
	if recErr := attemptPodmanRecovery(recoveryCtx, wrapperPath); recErr != nil {
		ginkgo.GinkgoWriter.Printf("[podman-recovery] cleanup recovery failed: %v\n", recErr)
		return cleanupErr
	}
	retryErr := f.CleanupWorkspace(ctx, dirs.tempDir)
	if retryErr != nil {
		ginkgo.GinkgoWriter.Printf(
			"[podman-recovery] cleanup retry after daemon restart still failed for %s: %v\n",
			dirs.tempDir,
			retryErr,
		)
		return retryErr
	}
	ginkgo.GinkgoWriter.Printf(
		"[podman-recovery] cleanup retry succeeded after daemon restart for %s\n",
		dirs.tempDir,
	)
	return nil
}
