//go:build !windows

package ide

import (
	"context"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/ide/opener"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

// setupBrowserIDE prepares a docker provider + workspace tempdir and registers
// the standard cleanup deferred to DeferCleanup. It returns the framework and
// the workspace tempDir path.
func setupBrowserIDE(ctx context.Context, initialDir string) (*framework.Framework, string) {
	f := framework.NewDefaultFramework(initialDir + "/bin")
	tempDir, err := framework.CopyToTempDir("tests/ide/testdata")
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

	err = f.DevsyProviderAdd(ctx, "docker")
	framework.ExpectNoError(err)
	err = f.DevsyProviderUse(ctx, "docker")
	framework.ExpectNoError(err)

	ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
		// Best-effort cleanup; ignore errors since some tests delete the
		// workspace before this fires.
		_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
	})

	return f, tempDir
}

// upBrowserIDE runs `devsy up --ide=openvscode --ide-option OPEN=false` against
// tempDir, optionally with extra args (e.g. "--recreate"). It returns the
// resolved workspace's tunnel state, which is asserted non-nil.
func upBrowserIDE(
	ctx context.Context, f *framework.Framework, tempDir string, extraArgs ...string,
) *opener.TunnelState {
	args := []string{"--ide=openvscode", "--ide-option", "OPEN=false"}
	args = append(args, extraArgs...)
	args = append(args, tempDir)
	err := f.DevsyUpWithIDE(ctx, args...)
	framework.ExpectNoError(err)

	ws, err := f.FindWorkspace(ctx, tempDir)
	framework.ExpectNoError(err)
	gomega.Expect(ws).NotTo(gomega.BeNil())
	gomega.Expect(ws.Context).NotTo(gomega.BeEmpty())

	state, err := opener.ReadTunnelState(ws.Context, ws.ID)
	framework.ExpectNoError(err)
	gomega.Expect(state).NotTo(gomega.BeNil(),
		"expected tunnel.json to exist for browser IDE")
	gomega.Expect(state.PID).To(gomega.BeNumerically(">", 0))
	return state
}

var _ = ginkgo.Describe(
	"devsy up browser IDE returns instead of blocking",
	ginkgo.Label("ide"),
	ginkgo.Ordered,
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It(
			"spawns a detached browser tunnel and returns",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				f, tempDir := setupBrowserIDE(ctx, initialDir)

				// Run up with a browser IDE. --ide-option OPEN=false suppresses the
				// host browser launch (no display available in CI) but still runs
				// openIDE → startDetachedBrowserTunnel → writes tunnel.json.
				// --open-ide=false would skip openIDE entirely, which is not what
				// the test exercises. With the old blocking behavior this would
				// still hang past SpecTimeout; with the new behavior the CLI
				// returns.
				state := upBrowserIDE(ctx, f, tempDir)
				gomega.Expect(state.Label).To(gomega.Equal("vscode"))
				gomega.Expect(strings.HasPrefix(state.TargetURL, "http://localhost:")).
					To(gomega.BeTrue(),
						"expected TargetURL to start with http://localhost:, got %s", state.TargetURL)

				// Verify the helper PID is alive: signal 0 returns nil if the
				// process exists.
				err := syscall.Kill(state.PID, 0)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(),
					"expected helper PID %d to be alive", state.PID)

				ws, err := f.FindWorkspace(ctx, tempDir)
				framework.ExpectNoError(err)

				// Stop the workspace; the tunnel state should be cleaned up.
				err = f.DevsyStop(ctx, tempDir)
				framework.ExpectNoError(err)

				stateAfter, err := opener.ReadTunnelState(ws.Context, ws.ID)
				framework.ExpectNoError(err)
				gomega.Expect(stateAfter).To(gomega.BeNil(),
					"expected tunnel.json to be removed after devsy stop")
			},
		)

		ginkgo.It(
			"devsy stop cleans up tunnel.json after the helper was killed externally",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				f, tempDir := setupBrowserIDE(ctx, initialDir)

				state := upBrowserIDE(ctx, f, tempDir)
				pid := state.PID

				// Externally kill the helper to simulate a stale state file
				// (e.g. machine reboot, OOM, manual kill).
				err := syscall.Kill(pid, syscall.SIGKILL)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(),
					"expected to be able to SIGKILL helper PID %d", pid)

				// Wait briefly for the helper to actually die.
				gomega.Eventually(func() error {
					return syscall.Kill(pid, 0)
				}).WithTimeout(5*time.Second).WithPolling(100*time.Millisecond).
					Should(gomega.HaveOccurred(),
						"expected helper PID %d to be dead after SIGKILL", pid)

				ws, err := f.FindWorkspace(ctx, tempDir)
				framework.ExpectNoError(err)

				// devsy stop must tolerate the stale state file: no error,
				// and the tunnel.json should be cleaned up.
				err = f.DevsyStop(ctx, tempDir)
				framework.ExpectNoError(err)

				stateAfter, err := opener.ReadTunnelState(ws.Context, ws.ID)
				framework.ExpectNoError(err)
				gomega.Expect(stateAfter).To(gomega.BeNil(),
					"expected tunnel.json to be removed after devsy stop even with dead helper")
			},
		)

		ginkgo.It(
			"devsy up --recreate kills the existing tunnel and respawns a fresh helper",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				f, tempDir := setupBrowserIDE(ctx, initialDir)

				state1 := upBrowserIDE(ctx, f, tempDir)
				pid1 := state1.PID

				// Sanity: PID1 alive before recreate.
				gomega.Expect(syscall.Kill(pid1, 0)).NotTo(gomega.HaveOccurred(),
					"expected helper PID %d to be alive before --recreate", pid1)

				// Recreate the workspace; the existing browser tunnel must be
				// killed before a new helper is spawned.
				state2 := upBrowserIDE(ctx, f, tempDir, "--recreate")
				pid2 := state2.PID

				gomega.Expect(pid2).NotTo(gomega.Equal(pid1),
					"expected --recreate to spawn a new helper (PID1=%d, PID2=%d)", pid1, pid2)

				// PID1 should now be dead. Use Eventually because the kill is
				// best-effort SIGTERM → wait → SIGKILL.
				gomega.Eventually(func() error {
					return syscall.Kill(pid1, 0)
				}).WithTimeout(5*time.Second).WithPolling(100*time.Millisecond).
					Should(gomega.HaveOccurred(),
						"expected old helper PID %d to be dead after --recreate", pid1)

				// PID2 should be alive.
				gomega.Expect(syscall.Kill(pid2, 0)).NotTo(gomega.HaveOccurred(),
					"expected new helper PID %d to be alive after --recreate", pid2)

				err := f.DevsyStop(ctx, tempDir)
				framework.ExpectNoError(err)
			},
		)
	},
)
