package ssh

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const tunnelActiveTimeout = 4 * time.Minute

// readTunnelSSHConfig waits for the worker to write the SSH config: the
// ready phase can win the race against the config write on slower runners.
func readTunnelSSHConfig(ctx context.Context, path string) string {
	var configBytes []byte
	gomega.Eventually(func() error {
		var err error
		configBytes, err = os.ReadFile(filepath.Clean(path))
		return err
	}).WithContext(ctx).WithTimeout(time.Minute).WithPolling(2 * time.Second).
		Should(gomega.Succeed())
	return string(configBytes)
}

var _ = ginkgo.Describe(
	"devsy ssh tunnel mode",
	ginkgo.Label("ssh-tunnel-mode"),
	ginkgo.Ordered,
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It("should start workspace with --ssh-tunnel and SSH into it",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				tempDir, err := framework.CopyToTempDir("tests/ssh/testdata/local-test")
				framework.ExpectNoError(err)

				f := setupTunnelProvider(ctx, initialDir)

				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.CleanupTempDir(initialDir, tempDir)
				})

				taskID, err := startDetachedTunnelUp(ctx, f, tempDir)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceTaskCancel(cleanupCtx, taskID)
				})

				waitDetachedTunnelReady(ctx, f, taskID)

				devsySSHCtx, cancelSSH := context.WithDeadline(ctx, time.Now().Add(20*time.Second))
				defer cancelSSH()
				err = f.DevsySSHEchoTestString(devsySSHCtx, tempDir)
				framework.ExpectNoError(err)
			},
		)

		ginkgo.It("should write SSH config with Hostname and Port instead of ProxyCommand",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				tempDir, err := framework.CopyToTempDir("tests/ssh/testdata/local-test")
				framework.ExpectNoError(err)

				sshConfigDir := ginkgo.GinkgoT().TempDir()
				sshConfigPath := filepath.Join(sshConfigDir, "config")

				f := setupTunnelProvider(ctx, initialDir)

				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.CleanupTempDir(initialDir, tempDir)
				})

				taskID, err := startDetachedTunnelUp(ctx, f, tempDir, "--ssh-config", sshConfigPath)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceTaskCancel(cleanupCtx, taskID)
				})

				waitDetachedTunnelReady(ctx, f, taskID)

				config := readTunnelSSHConfig(ctx, sshConfigPath)

				gomega.Expect(config).To(
					gomega.ContainSubstring("Hostname 127.0.0.1"),
					"SSH config should use localhost hostname in tunnel mode",
				)
				gomega.Expect(config).To(
					gomega.MatchRegexp(`Port \d+`),
					"SSH config should contain a Port entry in tunnel mode",
				)
				gomega.Expect(config).NotTo(
					gomega.ContainSubstring("ProxyCommand"),
					"SSH config should not contain ProxyCommand in tunnel mode",
				)
			},
		)

		ginkgo.It("should establish a working local TCP tunnel listener",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				tempDir, err := framework.CopyToTempDir("tests/ssh/testdata/local-test")
				framework.ExpectNoError(err)

				sshConfigDir := ginkgo.GinkgoT().TempDir()
				sshConfigPath := filepath.Join(sshConfigDir, "config")

				f := setupTunnelProvider(ctx, initialDir)

				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.CleanupTempDir(initialDir, tempDir)
				})

				taskID, err := startDetachedTunnelUp(ctx, f, tempDir, "--ssh-config", sshConfigPath)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceTaskCancel(cleanupCtx, taskID)
				})

				waitDetachedTunnelReady(ctx, f, taskID)

				config := readTunnelSSHConfig(ctx, sshConfigPath)

				var port string
				for line := range strings.SplitSeq(config, "\n") {
					trimmed := strings.TrimSpace(line)
					if p, ok := strings.CutPrefix(trimmed, "Port "); ok {
						port = p
						break
					}
				}
				gomega.Expect(port).NotTo(gomega.BeEmpty(), "should find Port in SSH config")

				addr := net.JoinHostPort("127.0.0.1", port)
				gomega.Eventually(func() error {
					conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
					if err != nil {
						return err
					}
					return conn.Close()
				}).WithContext(ctx).WithTimeout(time.Minute).WithPolling(2*time.Second).
					Should(gomega.Succeed(), "should be able to connect to local tunnel port")
			},
		)

		ginkgo.It("should handle multiple sequential SSH commands via tunnel",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				tempDir, err := framework.CopyToTempDir("tests/ssh/testdata/local-test")
				framework.ExpectNoError(err)

				f := setupTunnelProvider(ctx, initialDir)

				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.CleanupTempDir(initialDir, tempDir)
				})

				taskID, err := startDetachedTunnelUp(ctx, f, tempDir)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceTaskCancel(cleanupCtx, taskID)
				})

				waitDetachedTunnelReady(ctx, f, taskID)

				runSSH := func(i int, budget time.Duration) (string, error) {
					sshCtx, cancelSSH := context.WithDeadline(ctx, time.Now().Add(budget))
					defer cancelSSH()
					return f.DevsySSH(sshCtx, tempDir, "echo iteration-"+strings.Repeat("x", i))
				}

				// The task phase mixes sub-pipeline events, so the first
				// command can still race the tunnel listener on a cold
				// container; later commands then exercise the warm path.
				var out string
				gomega.Eventually(func() error {
					var err error
					out, err = runSSH(0, time.Minute)
					return err
				}).WithContext(ctx).WithTimeout(3 * time.Minute).WithPolling(10 * time.Second).
					Should(gomega.Succeed())
				gomega.Expect(out).To(
					gomega.ContainSubstring("iteration-"),
					"sequential SSH command should succeed",
				)
				for i := 1; i < 3; i++ {
					out, err := runSSH(i, 20*time.Second)
					framework.ExpectNoError(err)
					gomega.Expect(out).To(
						gomega.ContainSubstring("iteration-"),
						"sequential SSH command should succeed",
					)
				}
			},
		)

		ginkgo.It("should fall back to ProxyCommand when tunnel mode is not enabled",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				tempDir, err := framework.CopyToTempDir("tests/ssh/testdata/local-test")
				framework.ExpectNoError(err)

				sshConfigDir := ginkgo.GinkgoT().TempDir()
				sshConfigPath := filepath.Join(sshConfigDir, "config")

				f := setupTunnelProvider(ctx, initialDir)

				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.CleanupTempDir(initialDir, tempDir)
				})

				devsyUpCtx, cancel := context.WithDeadline(ctx, time.Now().Add(5*time.Minute))
				defer cancel()
				err = f.DevsyUp(devsyUpCtx, tempDir, "--ssh-config", sshConfigPath)
				framework.ExpectNoError(err)

				configBytes, err := os.ReadFile(filepath.Clean(sshConfigPath))
				framework.ExpectNoError(err)
				config := string(configBytes)

				gomega.Expect(config).To(
					gomega.ContainSubstring("ProxyCommand"),
					"SSH config should use ProxyCommand when tunnel mode is disabled",
				)
				gomega.Expect(config).NotTo(
					gomega.ContainSubstring("Hostname 127.0.0.1"),
					"SSH config should not have localhost hostname without tunnel mode",
				)
			},
		)
	},
)
