//go:build !windows

package ide

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/ide/opener"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const gpgTestKeyFingerprint = "07F681B9FD6C3411F679BFD1F51769DB572DDD3F"

// setupBrowserIDE prepares a docker provider and workspace tempdir and registers
// the standard cleanup deferred to DeferCleanup.
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
// tempDir, optionally with extra args.
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

				state2 := upBrowserIDE(ctx, f, tempDir, names.Flag(names.Recreate))
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
			"leaves ~/.local writable by a non-root remoteUser after code-server settings install",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")
				tempDir, err := framework.CopyToTempDir("tests/ide/testdata-codeserver-nonroot")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				err = f.DevsyProviderAdd(ctx, "docker")
				framework.ExpectNoError(err)
				err = f.DevsyProviderUse(ctx, "docker")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
				})

				err = f.DevsyUpWithIDE(ctx,
					"--ide=code-server", "--ide-launch=headless", tempDir)
				framework.ExpectNoError(err)

				for _, dir := range []string{"$HOME/.local", "$HOME/.local/share"} {
					owner, err := f.DevsySSH(ctx, tempDir, "stat -c %U "+dir)
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(owner)).To(gomega.Equal("devsyuser"),
						"%s should be owned by the remote user, not root", dir)
				}

				out, err := f.DevsySSH(ctx, tempDir,
					"mkdir -p $HOME/.local/lib && echo ok")
				framework.ExpectNoError(err)
				gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("ok"),
					"remote user should be able to create ~/.local/lib")

				framework.ExpectNoError(f.DevsyStop(ctx, tempDir))
			},
		)

		ginkgo.It(
			"does not collide on shared /tmp coordination files when GPG forwarding "+
				"races the browser-IDE tunnel under a non-root remoteUser",
			ginkgo.Label("gpg"),
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")
				tempDir, err := framework.CopyToTempDir("tests/ide/testdata-gpg-nonroot")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				err = f.DevsyProviderAdd(ctx, "docker")
				framework.ExpectNoError(err)
				err = f.DevsyProviderUse(ctx, "docker")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
				})

				ginkgo.GinkgoT().Setenv("GNUPGHOME", ginkgo.GinkgoT().TempDir())
				framework.ExpectNoError(
					framework.ImportGpgKey(
						filepath.Join(
							initialDir,
							"tests/ssh/testdata/gpg-forwarding/gpg-private.key",
						),
					),
				)

				stdout, stderr, err := f.DevsyUpStreamsRaw(ctx, tempDir,
					"--ide=openvscode", "--ide-launch=headless",
					names.Flag(names.SSHGPGForwarding), "--debug")
				framework.ExpectNoError(err)
				combined := stdout + stderr

				sshCtx, cancelSSH := context.WithDeadline(ctx, time.Now().Add(30*time.Second))
				defer cancelSSH()
				err = f.DevsySSHGpgSecretKeyForwarded(sshCtx, tempDir, gpgTestKeyFingerprint)
				framework.ExpectNoError(err)

				// After functional GPG forwarding succeeded, inspect both CLI and helper logs for issues.
				tunnelLogs := getTunnelLogs(combined)()
				allLogs := combined + "\n--- helper logs ---\n" + tunnelLogs

				gomega.Expect(allLogs).NotTo(gomega.ContainSubstring("permission denied"),
					"root and vscode sessions must not collide on /tmp/devsy-gpg-setup.lock; "+
						"got:\n%s", allLogs)
				gomega.Expect(allLogs).NotTo(gomega.ContainSubstring("operation not permitted"),
					"root and vscode sessions must not collide on /tmp/devsy.activity; "+
						"got:\n%s", allLogs)
				gomega.Expect(allLogs).NotTo(gomega.ContainSubstring("continuing without it"),
					"GPG agent forwarding must succeed end-to-end for a non-root remoteUser "+
						"browser IDE; got:\n%s", allLogs)

				framework.ExpectNoError(f.DevsyStop(ctx, tempDir))
			},
		)

		ginkgo.It(
			"forwards GPG in browser IDE when enabled only via context option",
			ginkgo.Label("gpg"),
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")
				tempDir, err := framework.CopyToTempDir("tests/ide/testdata-gpg-nonroot")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				contextName := fmt.Sprintf("gpg-ctx-%d", time.Now().UnixNano())
				err = f.DevsyContextCreate(ctx, contextName)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyContextUse(cleanupCtx, config.DefaultContext)
					_ = f.DevsyContextDelete(cleanupCtx, contextName)
				})

				err = f.DevsyContextUse(ctx, contextName)
				framework.ExpectNoError(err)
				err = f.ExecCommand(
					ctx,
					false,
					true,
					"",
					[]string{
						"context",
						"set",
						names.Flag(names.Option),
						config.ContextOptionGPGAgentForwarding + "=" + config.BoolTrue,
					},
				)
				framework.ExpectNoError(err)

				err = f.DevsyProviderAdd(ctx, "docker")
				framework.ExpectNoError(err)
				err = f.DevsyProviderUse(ctx, "docker")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
				})

				ginkgo.GinkgoT().Setenv("GNUPGHOME", ginkgo.GinkgoT().TempDir())
				framework.ExpectNoError(
					framework.ImportGpgKey(
						filepath.Join(
							initialDir,
							"tests/ssh/testdata/gpg-forwarding/gpg-private.key",
						),
					),
				)

				stdout, stderr, err := f.DevsyUpStreamsRaw(ctx, tempDir,
					"--ide=openvscode", "--ide-launch=headless", "--debug")
				framework.ExpectNoError(err)
				combined := stdout + stderr
				gomega.Expect(combined).
					To(gomega.ContainSubstring("starting vscode in browser mode at"),
						"expected browser IDE opener path to execute; got:\n%s", combined)

				// Since forwarding runs asynchronously in the helper process, poll its logs for readiness.
				gomega.Eventually(getTunnelLogs(combined)).
					WithTimeout(15*time.Second).
					WithPolling(200*time.Millisecond).
					Should(gomega.ContainSubstring("forwarding gpg-agent"),
						"expected browser IDE GPG forward bootstrap to run; got:\n%s", combined)

				sshCtx, cancelSSH := context.WithDeadline(ctx, time.Now().Add(30*time.Second))
				defer cancelSSH()
				err = f.DevsySSHGpgSecretKeyForwarded(sshCtx, tempDir, gpgTestKeyFingerprint)
				framework.ExpectNoError(err)

				tunnelLogs := getTunnelLogs(combined)()
				allLogs := combined + "\n--- helper logs ---\n" + tunnelLogs

				gomega.Expect(allLogs).NotTo(gomega.ContainSubstring("continuing without it"),
					"GPG agent forwarding must succeed when enabled from context option only; got:\n%s", allLogs)
				gomega.Expect(allLogs).NotTo(gomega.ContainSubstring(
					"timed out waiting for gpg-agent forward to become ready",
				), "GPG forward should be ready before handing off browser IDE session; got:\n%s", allLogs)

				ws, err := f.FindWorkspace(ctx, tempDir)
				framework.ExpectNoError(err)
				state, err := opener.ReadTunnelState(ws.Context, ws.ID)
				framework.ExpectNoError(err)
				gomega.Expect(state).NotTo(gomega.BeNil(),
					"expected browser tunnel state to exist for IDE session")
				gomega.Expect(state.PID).To(gomega.BeNumerically(">", 0),
					"expected detached browser tunnel process to be running")

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

// getTunnelLogs parses the log file location from the combined stdout/stderr output of the CLI,
// and returns a function that dynamically reads and returns the contents of that log file when called.
func getTunnelLogs(combined string) func() string {
	idx := strings.Index(combined, "Logs: ")
	if idx < 0 {
		return func() string { return "" }
	}
	start := idx + len("Logs: ")
	end := strings.Index(combined[start:], ". Run 'devsy")
	if end < 0 {
		return func() string { return "" }
	}
	logPath := combined[start : start+end]
	return func() string {
		data, err := os.ReadFile(logPath) // #nosec G304: path derived from test workspace
		if err != nil {
			return ""
		}
		return string(data)
	}
}
