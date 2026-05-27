//go:build !windows

package ide

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"runtime"
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

// upBrowserIDE runs `devsy up --ide=openvscode --ide-launch=headless` against
// tempDir, optionally with extra args (e.g. "--recreate"). It returns the
// resolved workspace's tunnel state, which is asserted non-nil.
func upBrowserIDE(
	ctx context.Context, f *framework.Framework, tempDir string, extraArgs ...string,
) *opener.TunnelState {
	args := []string{"--ide=openvscode", "--ide-launch=headless"}
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

				// Run up with a browser IDE. --ide-launch=headless suppresses the
				// host browser launch (no display available in CI) but still runs
				// openIDE → startDetachedBrowserTunnel → writes tunnel.json.
				// --ide-launch=skip would skip openIDE entirely, which is not what
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

		ginkgo.It(
			"browser-tunnel port-forward survives past the old 5s idle timeout",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				f, tempDir := setupBrowserIDE(ctx, initialDir)
				state := upBrowserIDE(ctx, f, tempDir)

				// Parse host:port from state.TargetURL (e.g.
				// http://localhost:10800/?folder=...).
				u, err := url.Parse(state.TargetURL)
				framework.ExpectNoError(err)
				addr := u.Host

				// First wait for the listener to actually bind. Use Eventually
				// so a slow cold-start (which the 30s probe budget is sized
				// for) doesn't race the assertion.
				gomega.Eventually(func() error {
					c, dialErr := net.DialTimeout("tcp", addr, 1*time.Second)
					if dialErr == nil {
						_ = c.Close()
					}
					return dialErr
				}).WithTimeout(30*time.Second).WithPolling(500*time.Millisecond).
					Should(gomega.Succeed(), "helper port-forward at %s never came up", addr)

				// Now sleep past the OLD 5s idle window with NO connections.
				time.Sleep(8 * time.Second)

				// And the listener must still accept.
				conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(),
					"expected helper port-forward at %s to still accept after 8s idle", addr)
				if conn != nil {
					_ = conn.Close()
				}

				framework.ExpectNoError(f.DevsyStop(ctx, tempDir))
			},
		)

		ginkgo.It(
			"passes --open-browser to the helper when --ide-launch=auto",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				if runtime.GOOS != "linux" {
					ginkgo.Skip("/proc/<pid>/cmdline inspection requires Linux")
				}
				f, tempDir := setupBrowserIDE(ctx, initialDir)

				err := f.DevsyUpWithIDE(ctx,
					"--ide=openvscode", "--ide-launch=auto", tempDir,
				)
				framework.ExpectNoError(err)

				ws, err := f.FindWorkspace(ctx, tempDir)
				framework.ExpectNoError(err)
				gomega.Expect(ws).NotTo(gomega.BeNil())
				state, err := opener.ReadTunnelState(ws.Context, ws.ID)
				framework.ExpectNoError(err)
				gomega.Expect(state).NotTo(gomega.BeNil(),
					"expected tunnel.json to exist for browser IDE")
				gomega.Expect(state.PID).To(gomega.BeNumerically(">", 0))

				// Read the helper's argv from /proc.
				cmdlineBytes, err := os.ReadFile(
					fmt.Sprintf("/proc/%d/cmdline", state.PID),
				)
				framework.ExpectNoError(err)
				cmdlineBytes = bytes.TrimRight(cmdlineBytes, "\x00")
				args2 := strings.Split(string(cmdlineBytes), "\x00")

				gomega.Expect(args2).To(gomega.ContainElement("--open-browser"),
					"expected helper to be launched with --open-browser, got args: %v", args2)

				framework.ExpectNoError(f.DevsyStop(ctx, tempDir))
			},
		)

		ginkgo.It(
			"does not log 'setup KubeConfig' on a workspace without kubeconfig forwarding",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				f, tempDir := setupBrowserIDE(ctx, initialDir)
				stdout, stderr, err := f.DevsyUpStreamsRaw(ctx, tempDir,
					"--ide=openvscode", "--ide-launch=headless", "--debug")
				framework.ExpectNoError(err)
				combined := stdout + stderr

				// The line "setup KubeConfig" used to fire unconditionally.
				// After the fix it only fires when the host actually has a
				// config to forward, which is not configured for the default
				// test workspace.
				gomega.Expect(combined).NotTo(gomega.ContainSubstring("setup KubeConfig"),
					"should not log 'setup KubeConfig' when host has no kubeconfig to forward")

				framework.ExpectNoError(f.DevsyStop(ctx, tempDir))
			},
		)
	},
)
