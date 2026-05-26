package ssh

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

// Regression tests for the per-session agent-forwarding socket bug.
// Previously the SSH server allocated a fresh agent-forwarding socket per SSH
// session, so $SSH_AUTH_SOCK changed between sessions on the same connection
// and stale sockets leaked. The fix allocates one socket per CONNECTION and
// cleans it up on connection close — these specs lock that contract in place.
var _ = ginkgo.Describe(
	"devsy ssh agent forwarding",
	ginkgo.Label("ssh"),
	ginkgo.Label("agent-forward"),
	ginkgo.Ordered,
	func() {
		var (
			initialDir string
			tempDir    string
			f          *framework.Framework
			authSock   string
			pubKey     string
			agentClean func()
			host       string
		)

		ginkgo.BeforeAll(func(ctx ginkgo.SpecContext) {
			if runtime.GOOS == osWindows {
				ginkgo.Skip("UNIX sockets required; skipping on windows")
			}
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)

			tempDir, err = framework.CopyToTempDir("tests/ssh/testdata/agent-forward")
			framework.ExpectNoError(err)

			f = framework.NewDefaultFramework(initialDir + "/bin")
			_ = f.DevsyProviderAdd(ctx, "docker")
			err = f.DevsyProviderUse(ctx, "docker")
			framework.ExpectNoError(err)

			authSock, pubKey, agentClean = framework.StartMockSSHAgent(ginkgo.GinkgoT())

			devsyUpCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			err = f.DevsyUp(devsyUpCtx, tempDir)
			framework.ExpectNoError(err)

			// devsy up registers an OpenSSH host alias "<workspace>.devsy".
			host = filepath.Base(tempDir) + ".devsy"
		})

		ginkgo.AfterAll(func(ctx ginkgo.SpecContext) {
			if f != nil && tempDir != "" {
				_ = f.DevsyWorkspaceDelete(ctx, tempDir)
				framework.CleanupTempDir(initialDir, tempDir)
			}
			if agentClean != nil {
				agentClean()
			}
		})

		ginkgo.It(
			"socket is stable across sessions on the same connection",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(_ ginkgo.SpecContext) {
				controlPath, closeCM, err := framework.OpenSSHControlMaster(
					ginkgo.GinkgoT(),
					host,
					map[string]string{envSSHAuthSock: authSock},
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(closeCM)

				env := map[string]string{envSSHAuthSock: authSock}

				// Session A: capture $SSH_AUTH_SOCK inside the container and
				// verify the forwarded key is reachable.
				outA, errA, err := framework.SSHMultiplexedExec(
					controlPath, host, env,
					"sh", "-c", "printf %s \"$SSH_AUTH_SOCK\"",
				)
				ginkgo.GinkgoWriter.Printf("session A stderr: %s\n", errA)
				framework.ExpectNoError(err)
				s1 := strings.TrimSpace(outA)
				gomega.Expect(s1).NotTo(gomega.BeEmpty(), "session A must see SSH_AUTH_SOCK")

				outAddA, _, err := framework.SSHMultiplexedExec(
					controlPath, host, env,
					"ssh-add", "-L",
				)
				framework.ExpectNoError(err)
				gomega.Expect(outAddA).To(
					gomega.ContainSubstring(strings.Fields(pubKey)[1]),
					"session A: forwarded agent must expose our test pubkey",
				)

				// Session B: own env shows a socket file, and S1 from A is
				// STILL alive (proves stability across sessions).
				_, _, err = framework.SSHMultiplexedExec(
					controlPath, host, env,
					"sh", "-c", "test -S \"$SSH_AUTH_SOCK\"",
				)
				framework.ExpectNoError(
					err,
					"session B: its own SSH_AUTH_SOCK must be a live socket",
				)

				outAddB, errB, err := framework.SSHMultiplexedExec(
					controlPath, host, env,
					"sh", "-c", "SSH_AUTH_SOCK="+s1+" ssh-add -L",
				)
				ginkgo.GinkgoWriter.Printf("session B explicit-S1 stderr: %s\n", errB)
				framework.ExpectNoError(err, "session B: socket from session A must still work")
				gomega.Expect(outAddB).To(gomega.ContainSubstring(strings.Fields(pubKey)[1]))

				// Session B's reported SSH_AUTH_SOCK should equal S1 too — the
				// regression is that this used to differ session-to-session.
				outBSock, _, err := framework.SSHMultiplexedExec(
					controlPath, host, env,
					"sh", "-c", "printf %s \"$SSH_AUTH_SOCK\"",
				)
				framework.ExpectNoError(err)
				gomega.Expect(strings.TrimSpace(outBSock)).To(
					gomega.Equal(s1),
					"SSH_AUTH_SOCK must be identical across sessions on one connection",
				)

				// Session C: open-remote-ssh style use-after-session pattern —
				// reuse S1 in yet another session.
				outAddC, _, err := framework.SSHMultiplexedExec(
					controlPath, host, env,
					"sh", "-c", "SSH_AUTH_SOCK="+s1+" ssh-add -L",
				)
				framework.ExpectNoError(err, "session C: S1 must still work")
				gomega.Expect(outAddC).To(gomega.ContainSubstring(strings.Fields(pubKey)[1]))
			},
		)

		ginkgo.It(
			"each connection gets its own socket",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(_ ginkgo.SpecContext) {
				env := map[string]string{envSSHAuthSock: authSock}

				cp1, close1, err := framework.OpenSSHControlMaster(ginkgo.GinkgoT(), host, env)
				framework.ExpectNoError(err)
				closed1 := false
				ginkgo.DeferCleanup(func() {
					if !closed1 {
						close1()
					}
				})

				cp2, close2, err := framework.OpenSSHControlMaster(ginkgo.GinkgoT(), host, env)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(close2)

				out1, _, err := framework.SSHMultiplexedExec(
					cp1, host, env, "sh", "-c", "printf %s \"$SSH_AUTH_SOCK\"",
				)
				framework.ExpectNoError(err)
				sock1 := strings.TrimSpace(out1)

				out2, _, err := framework.SSHMultiplexedExec(
					cp2, host, env, "sh", "-c", "printf %s \"$SSH_AUTH_SOCK\"",
				)
				framework.ExpectNoError(err)
				sock2 := strings.TrimSpace(out2)

				gomega.Expect(sock1).NotTo(gomega.BeEmpty())
				gomega.Expect(sock2).NotTo(gomega.BeEmpty())
				gomega.Expect(sock1).NotTo(
					gomega.Equal(sock2),
					"two independent connections must each get their own agent socket",
				)

				// Close connection 1 — connection 2 must still work.
				close1()
				closed1 = true

				outAdd, _, err := framework.SSHMultiplexedExec(
					cp2, host, env, "ssh-add", "-L",
				)
				framework.ExpectNoError(
					err,
					"connection 2 must remain functional after connection 1 closes",
				)
				gomega.Expect(outAdd).To(gomega.ContainSubstring(strings.Fields(pubKey)[1]))
			},
		)

		ginkgo.It(
			"socket directory is cleaned up after connection close",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(_ ginkgo.SpecContext) {
				env := map[string]string{envSSHAuthSock: authSock}

				cp, closeCM, err := framework.OpenSSHControlMaster(ginkgo.GinkgoT(), host, env)
				framework.ExpectNoError(err)
				closed := false
				ginkgo.DeferCleanup(func() {
					if !closed {
						closeCM()
					}
				})

				out, _, err := framework.SSHMultiplexedExec(
					cp, host, env, "sh", "-c", "printf %s \"$SSH_AUTH_SOCK\"",
				)
				framework.ExpectNoError(err)
				sockPath := strings.TrimSpace(out)
				gomega.Expect(sockPath).NotTo(gomega.BeEmpty())

				// Confirm the socket exists from inside the container before close.
				_, _, err = framework.SSHMultiplexedExec(
					cp, host, env, "sh", "-c", "test -S "+sockPath,
				)
				framework.ExpectNoError(err, "socket must exist while connection is open")

				closeCM()
				closed = true

				// The cleanup hop traverses the devsy proxy → in-container
				// SSH server's ctx.Done(), which can take several seconds
				// under CI load. Each devsy ssh observation runs on a fresh
				// connection so the just-closed socket's filesystem state is
				// always up-to-date.
				gomega.Eventually(func() string {
					out, _ := f.DevsySSH(
						context.Background(),
						tempDir,
						"test -S "+sockPath+" && echo PRESENT || echo GONE",
					)
					return out
				}).WithTimeout(30*time.Second).WithPolling(500*time.Millisecond).
					Should(
						gomega.ContainSubstring("GONE"),
						"socket %s must be cleaned up after connection close",
						sockPath,
					)
			},
		)

		ginkgo.It(
			"connection without any agent request still cleans up",
			ginkgo.Label("ssh"),
			ginkgo.Label("agent-forward"),
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(_ ginkgo.SpecContext) {
				tmpDir, err := os.MkdirTemp("", "devsy-ssh-cm-clean-")
				framework.ExpectNoError(err)
				controlPath := filepath.Join(tmpDir, "cm.sock")
				ginkgo.DeferCleanup(func() {
					_ = os.RemoveAll(tmpDir)
				})

				startArgs := []string{
					"-o", sshOptStrictHostKeyCheckingNo,
					"-o", sshOptUserKnownHostsFileNull,
					"-o", "ControlMaster=yes",
					"-o", "ControlPath=" + controlPath,
					"-o", "ControlPersist=120",
					"-o", sshOptForwardAgentNo,
					"-N", "-f",
					host,
				}
				// #nosec G204
				startCmd := exec.Command("ssh", startArgs...)
				startCmd.Env = append(os.Environ(), "SSH_AUTH_SOCK="+authSock)
				combined, startErr := startCmd.CombinedOutput()
				framework.ExpectNoError(
					startErr,
					"ssh ControlMaster start (no agent): %s",
					combined,
				)

				// Run a trivial command without requesting forwarding.
				trivialArgs := []string{
					"-o", "ControlPath=" + controlPath,
					"-o", sshOptStrictHostKeyCheckingNo,
					"-o", sshOptUserKnownHostsFileNull,
					"-o", sshOptForwardAgentNo,
					host, "--",
					"true",
				}
				// #nosec G204
				trivCmd := exec.Command("ssh", trivialArgs...)
				trivCmd.Env = append(os.Environ(), "SSH_AUTH_SOCK="+authSock)
				trivOut, trivErr := trivCmd.CombinedOutput()
				framework.ExpectNoError(trivErr, "trivial session: %s", trivOut)

				// Close the master connection.
				// #nosec G204
				exitCmd := exec.Command("ssh", "-O", "exit",
					"-o", "ControlPath="+controlPath, host)
				exitCmd.Env = append(os.Environ(), "SSH_AUTH_SOCK="+authSock)
				_ = exitCmd.Run()

				// On a fresh devsy ssh connection, assert no auth-agent-conn-*
				// directories remain. With lazy allocation, none are ever
				// created; with the cleanup goroutine, any leftover is removed.
				gomega.Eventually(func() string {
					out, _ := f.DevsySSH(
						context.Background(),
						tempDir,
						"sh -c 'ls -d \"$XDG_RUNTIME_DIR\"/auth-agent-conn-* 2>/dev/null | wc -l'",
					)
					return strings.TrimSpace(out)
				}).WithTimeout(30*time.Second).WithPolling(500*time.Millisecond).
					Should(
						gomega.Equal("0"),
						"no auth-agent-conn-* dir must remain after a no-forward connection closes",
					)
			},
		)

		ginkgo.It(
			"parallel sessions on one connection observe the same socket concurrently",
			ginkgo.Label("agent-forward"),
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(_ ginkgo.SpecContext) {
				controlPath, closeCM, err := framework.OpenSSHControlMaster(
					ginkgo.GinkgoT(),
					host,
					map[string]string{envSSHAuthSock: authSock},
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(closeCM)

				env := map[string]string{envSSHAuthSock: authSock}

				const n = 4
				var wg sync.WaitGroup
				results := make([]string, n)
				errs := make([]error, n)
				for i := range n {
					wg.Add(1)
					go func(idx int) {
						defer wg.Done()
						out, _, runErr := framework.SSHMultiplexedExec(
							controlPath, host, env,
							"sh", "-c", "printf %s \"$SSH_AUTH_SOCK\"",
						)
						results[idx] = strings.TrimSpace(out)
						errs[idx] = runErr
					}(i)
				}
				wg.Wait()

				for i, e := range errs {
					framework.ExpectNoError(e, fmt.Sprintf("session %d failed", i))
				}
				first := results[0]
				gomega.Expect(first).NotTo(gomega.BeEmpty())
				for i, r := range results {
					gomega.Expect(r).To(
						gomega.Equal(first),
						fmt.Sprintf("session %d saw %q, expected %q", i, r, first),
					)
				}
			},
		)
	},
)
