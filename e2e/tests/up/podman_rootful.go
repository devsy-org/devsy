package up

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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
	podmanHealthCheckTimeout = 10 * time.Second
	podmanRecoveryTimeout    = 30 * time.Second
	podmanDiagnosticsBudget  = 25 * time.Second
	podmanDiagCommandTimeout = 5 * time.Second
	// podmanDiagMaxOutput caps ordinary diagnostic sections.
	podmanDiagMaxOutput = 8 * 1024
	// podmanProcessLockDiagMaxOutput preserves the complete process/lock inventory.
	podmanProcessLockDiagMaxOutput = 32 * 1024
	// podmanBinName is the fallback binary when the rootful wrapper is absent.
	podmanBinName            = "podman"
	podmanRootfulWrapperName = "podman-rootful"
	podmanSudoCommand        = "sudo"
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
	podmanHealthPoisoned
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
	case podmanHealthPoisoned:
		return "poisoned"
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

func rootfulPodmanUsesLocalDaemon(endpoint string) bool {
	if endpoint == "" {
		return true
	}
	parsed, err := url.Parse(endpoint)
	return err == nil && parsed.Scheme == "unix" && parsed.Host == "" && parsed.Path != ""
}

func rootfulPodmanWrapperScript() string {
	return `#!/bin/sh
set -eu

if [ -n "${DOCKER_HOST:-}" ]; then
	exec sudo podman --remote --url "$DOCKER_HOST" "$@"
fi

exec sudo podman "$@"
`
}

// podmanDaemonGate records the first unrecoverable daemon failure so later
// specs in the shard skip instead of cascading into identical infrastructure
// failures that would bury the actionable one. Scoped to the test process,
// which runs exactly one shard in CI.
type podmanDaemonGate struct {
	mu             sync.Mutex
	unhealthySince string
	recoveryUsed   bool
}

func (g *podmanDaemonGate) claimRecovery() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.recoveryUsed {
		return false
	}
	g.recoveryUsed = true
	return true
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
func runDiagCommand(ctx context.Context, name string, args ...string) string {
	diagCtx, cancel := context.WithTimeout(ctx, podmanDiagCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext( //nolint:gosec // G204: fixed diagnostic commands
		diagCtx,
		name,
		args...,
	)
	docker.PrepareForGroupCancellation(cmd)
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil {
		text += fmt.Sprintf("\n(command failed: %v; context err: %v)", err, diagCtx.Err())
	}
	limit := podmanDiagMaxOutput
	if name == "ps" || name == "lslocks" || name == "sh" ||
		strings.Contains(strings.Join(args, " "), "lslocks") {
		limit = podmanProcessLockDiagMaxOutput
	}
	if len(text) > limit {
		text = text[:limit] + "\n... (truncated)"
	}
	return text
}

// collectPodmanDiagnostics prints bounded daemon state so the first failure
// in a shard carries the evidence needed to debug a wedge without a rerun.
func collectPodmanDiagnostics(wrapperPath string) {
	diagCtx, cancel := context.WithTimeout(context.Background(), podmanDiagnosticsBudget)
	defer cancel()
	ginkgo.GinkgoWriter.Printf(
		"[podman-diagnostics] total budget=%s\n", podmanDiagnosticsBudget,
	)
	sections := []podmanDiagSection{
		{
			"podman-related processes",
			"sh",
			[]string{
				"-c",
				"ps -eo pid,ppid,stat,wchan:32,etime,cmd | " +
					"grep -E 'podman|crun|conmon|fuse-overlayfs|netavark' | grep -v grep || true",
			},
		},
		{
			"container storage locks",
			podmanSudoCommand,
			[]string{"sh", "-c", "if command -v lslocks >/dev/null 2>&1; then " +
				"lslocks | grep -E '/var/lib/containers/storage|/run/containers/storage|" +
				"storage\\.lock|userns\\.lock|layers\\.lock|images\\.lock|db\\.sql' || true; " +
				"else cat /proc/locks; fi"},
		},
		{"stuck process details", "sh", []string{
			"-c", "for pid in $(ps -eo pid=,comm= | " +
				"awk '$2 ~ /^(podman|conmon|crun|fuse-overlayfs|netavark)$/ {print $1}'); " +
				"do echo PID=$pid; cat /proc/$pid/status /proc/$pid/wchan /proc/$pid/stack 2>&1; done",
		}},
		{
			"systemctl status podman.socket podman.service",
			podmanSudoCommand,
			[]string{
				"systemctl", "status", "podman.socket", "podman.service", "--no-pager", "-l",
			},
		},
		{
			"journalctl podman units (last 100 lines)",
			podmanSudoCommand,
			[]string{
				"journalctl", "-u", "podman.socket", "-u", "podman.service",
				"-n", "150", "--no-pager",
			},
		},
		{"disk usage", "df", []string{"-h", "/", "/var/lib/containers"}},
		{"memory", "free", []string{"-m"}},
		{"podman ps -a", wrapperPath, []string{"ps", "-a"}},
	}
	runPodmanDiagnostics(diagCtx, sections, runDiagCommand, func(title, output string) {
		ginkgo.GinkgoWriter.Printf("[podman-diagnostics] --- %s ---\n%s\n", title, output)
	})
}

type podmanDiagSection struct {
	title string
	name  string
	args  []string
}

func runPodmanDiagnostics(
	ctx context.Context,
	sections []podmanDiagSection,
	run func(context.Context, string, ...string) string,
	write func(string, string),
) {
	for _, section := range sections {
		if ctx.Err() != nil {
			write("budget exhausted", ctx.Err().Error())
			break
		}
		write(section.title, run(ctx, section.name, section.args...))
	}
}

// attemptPodmanRecovery performs the shard's one bounded restart of the
// rootful Podman socket and service, then re-probes health.
func attemptPodmanRecovery(ctx context.Context, wrapperPath string) (podmanHealthClass, error) {
	ginkgo.GinkgoWriter.Println(
		"[podman-recovery] attempting single bounded restart of podman.socket and podman.service",
	)
	restartCtx, cancel := context.WithTimeout(ctx, podmanRecoveryTimeout)
	defer cancel()
	cmd := exec.CommandContext( //nolint:gosec // G204: fixed recovery command
		restartCtx,
		podmanSudoCommand,
		"systemctl",
		"restart",
		"podman.socket",
		"podman.service",
	)
	docker.PrepareForGroupCancellation(cmd)
	started := time.Now()
	if out, err := cmd.CombinedOutput(); err != nil {
		return podmanHealthPoisoned, fmt.Errorf(
			"PODMAN_ROOTFUL_RECOVERY_FAILED: systemctl restart failed after %s: %w\noutput:\n%s",
			time.Since(started),
			err,
			string(out),
		)
	}
	ginkgo.GinkgoWriter.Printf("[podman-recovery] restart elapsed=%s\n", time.Since(started))
	class, err := checkPodmanHealth(ctx, wrapperPath)
	ginkgo.GinkgoWriter.Printf("[podman-health] post-recovery class=%s\n", class)
	if err != nil {
		return podmanHealthPoisoned, fmt.Errorf(
			"PODMAN_ROOTFUL_RUNTIME_POISONED: daemon still unhealthy after restart (class: %s): %w",
			class,
			err,
		)
	}
	return podmanHealthOK, nil
}

// setupRootfulPodman prepares the rootful Podman wrapper and docker provider
// and gates the shard on daemon health: once recovery has failed, remaining
// specs skip instead of re-failing on the same wedged infrastructure.
func setupRootfulPodman(ctx context.Context, initialDir string) *framework.Framework {
	wrapperPath := initialDir + "/bin/" + podmanRootfulWrapperName

	localDaemon := rootfulPodmanUsesLocalDaemon(os.Getenv("DOCKER_HOST"))
	if since := rootfulDaemonGate.unhealthy(); localDaemon && since != "" {
		ginkgo.Skip(fmt.Sprintf(
			"rootful Podman daemon unhealthy since first failure in %q; "+
				"skipping to avoid cascading infrastructure failures",
			since,
		))
	}

	wrapper, err := os.Create(wrapperPath) //nolint:gosec // G304: test-controlled path
	framework.ExpectNoError(err)

	_, err = wrapper.WriteString(rootfulPodmanWrapperScript())
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

	ginkgo.GinkgoWriter.Println("[podman-health] probe start")
	started := time.Now()
	class, healthErr := checkPodmanHealth(ctx, wrapperPath)
	ginkgo.GinkgoWriter.Printf("[podman-health] class=%s elapsed=%s\n", class, time.Since(started))
	if healthErr != nil {
		ginkgo.GinkgoWriter.Printf(
			"[podman-health] PODMAN_ROOTFUL_HEALTH_%s: %v\n",
			strings.ToUpper(class.String()),
			healthErr,
		)
		collectPodmanDiagnostics(wrapperPath)
		if localDaemon && shouldAttemptPodmanRecovery(class) {
			if !rootfulDaemonGate.claimRecovery() {
				rootfulDaemonGate.markUnhealthy(ginkgo.CurrentSpecReport().FullText())
				framework.ExpectNoError(
					fmt.Errorf(
						"PODMAN_ROOTFUL_RUNTIME_POISONED: recovery already used: %w",
						healthErr,
					),
				)
			}
			if postClass, recErr := attemptPodmanRecovery(ctx, wrapperPath); recErr != nil {
				rootfulDaemonGate.markUnhealthy(ginkgo.CurrentSpecReport().FullText())
				framework.ExpectNoError(fmt.Errorf(
					"PODMAN_ROOTFUL_RUNTIME_POISONED: rootful Podman daemon unhealthy; "+
						"single recovery attempt failed (class: %s): %w "+
						"(initial check: %v)",
					postClass,
					recErr,
					healthErr,
				))
			}
			ginkgo.GinkgoWriter.Println(
				"[podman-recovery] daemon healthy again after single restart",
			)
		} else {
			// Local service recovery cannot repair a remote endpoint or
			// a responsive daemon error, so fail without gating.
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
			runDiagCommand(context.Background(), podmanBinName, "ps", "-a"),
		)
		return cleanupErr
	}

	collectPodmanDiagnostics(wrapperPath)

	// The cleanup context may already be canceled after a spec timeout; use a
	// detached context so classification and recovery stay deterministic,
	// mirroring CleanupWorkspace.
	recoveryCtx := context.WithoutCancel(ctx)
	class, healthErr := checkPodmanHealth(recoveryCtx, wrapperPath)
	if healthErr == nil || !rootfulPodmanUsesLocalDaemon(os.Getenv("DOCKER_HOST")) ||
		!shouldAttemptPodmanRecovery(class) {
		return cleanupErr
	}
	if !rootfulDaemonGate.claimRecovery() {
		rootfulDaemonGate.markUnhealthy(ginkgo.CurrentSpecReport().FullText())
		return fmt.Errorf("PODMAN_ROOTFUL_RUNTIME_POISONED: recovery already used: %w", cleanupErr)
	}
	if postClass, recErr := attemptPodmanRecovery(recoveryCtx, wrapperPath); recErr != nil {
		rootfulDaemonGate.markUnhealthy(ginkgo.CurrentSpecReport().FullText())
		ginkgo.GinkgoWriter.Printf(
			"[podman-recovery] cleanup recovery failed (class: %s): %v\n",
			postClass,
			recErr,
		)
		return fmt.Errorf("%w: cleanup failed: %w", recErr, cleanupErr)
	}
	retryErr := f.CleanupWorkspace(ctx, dirs.tempDir)
	if retryErr != nil {
		if class, healthErr := checkPodmanHealth(
			recoveryCtx,
			wrapperPath,
		); healthErr != nil &&
			shouldAttemptPodmanRecovery(class) {
			rootfulDaemonGate.markUnhealthy(ginkgo.CurrentSpecReport().FullText())
			ginkgo.GinkgoWriter.Printf(
				"[podman-health] PODMAN_ROOTFUL_RUNTIME_POISONED after cleanup retry: %v\n",
				healthErr,
			)
		}
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
