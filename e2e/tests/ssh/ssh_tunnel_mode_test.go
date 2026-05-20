package ssh

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"devsy ssh tunnel mode",
	ginkgo.Label("ssh", "tunnel-mode"),
	ginkgo.Ordered,
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It("should start workspace with --ssh-tunnel-mode and SSH into it",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				if runtime.GOOS == osWindows {
					ginkgo.Skip("skipping on windows")
				}

				tempDir, err := framework.CopyToTempDir("tests/ssh/testdata/local-test")
				framework.ExpectNoError(err)

				f := framework.NewDefaultFramework(initialDir + "/bin")
				_ = f.DevsyProviderAdd(ctx, "docker")
				err = f.DevsyProviderUse(ctx, "docker")
				framework.ExpectNoError(err)

				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.CleanupTempDir(initialDir, tempDir)
				})

				devsyUpCtx, cancel := context.WithDeadline(ctx, time.Now().Add(5*time.Minute))
				defer cancel()
				err = f.DevsyUp(devsyUpCtx, tempDir, "--ssh-tunnel-mode")
				framework.ExpectNoError(err)

				devsySSHCtx, cancelSSH := context.WithDeadline(ctx, time.Now().Add(20*time.Second))
				defer cancelSSH()
				err = f.DevsySSHEchoTestString(devsySSHCtx, tempDir)
				framework.ExpectNoError(err)
			},
		)

		ginkgo.It("should write SSH config with Hostname and Port instead of ProxyCommand",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				if runtime.GOOS == osWindows {
					ginkgo.Skip("skipping on windows")
				}

				tempDir, err := framework.CopyToTempDir("tests/ssh/testdata/local-test")
				framework.ExpectNoError(err)

				sshConfigDir := ginkgo.GinkgoT().TempDir()
				sshConfigPath := filepath.Join(sshConfigDir, "config")

				f := framework.NewDefaultFramework(initialDir + "/bin")
				_ = f.DevsyProviderAdd(ctx, "docker")
				err = f.DevsyProviderUse(ctx, "docker")
				framework.ExpectNoError(err)

				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.CleanupTempDir(initialDir, tempDir)
				})

				devsyUpCtx, cancel := context.WithDeadline(ctx, time.Now().Add(5*time.Minute))
				defer cancel()
				err = f.DevsyUp(
					devsyUpCtx,
					tempDir,
					"--ssh-tunnel-mode",
					"--ssh-config",
					sshConfigPath,
				)
				framework.ExpectNoError(err)

				configBytes, err := os.ReadFile(filepath.Clean(sshConfigPath))
				framework.ExpectNoError(err)
				config := string(configBytes)

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
				if runtime.GOOS == osWindows {
					ginkgo.Skip("skipping on windows")
				}

				tempDir, err := framework.CopyToTempDir("tests/ssh/testdata/local-test")
				framework.ExpectNoError(err)

				sshConfigDir := ginkgo.GinkgoT().TempDir()
				sshConfigPath := filepath.Join(sshConfigDir, "config")

				f := framework.NewDefaultFramework(initialDir + "/bin")
				_ = f.DevsyProviderAdd(ctx, "docker")
				err = f.DevsyProviderUse(ctx, "docker")
				framework.ExpectNoError(err)

				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.CleanupTempDir(initialDir, tempDir)
				})

				devsyUpCtx, cancel := context.WithDeadline(ctx, time.Now().Add(5*time.Minute))
				defer cancel()
				err = f.DevsyUp(
					devsyUpCtx,
					tempDir,
					"--ssh-tunnel-mode",
					"--ssh-config",
					sshConfigPath,
				)
				framework.ExpectNoError(err)

				configBytes, err := os.ReadFile(filepath.Clean(sshConfigPath))
				framework.ExpectNoError(err)
				config := string(configBytes)

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
				conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(),
					"should be able to connect to local tunnel port",
				)
				_ = conn.Close()
			},
		)

		ginkgo.It("should handle multiple sequential SSH commands via tunnel",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				if runtime.GOOS == osWindows {
					ginkgo.Skip("skipping on windows")
				}

				tempDir, err := framework.CopyToTempDir("tests/ssh/testdata/local-test")
				framework.ExpectNoError(err)

				f := framework.NewDefaultFramework(initialDir + "/bin")
				_ = f.DevsyProviderAdd(ctx, "docker")
				err = f.DevsyProviderUse(ctx, "docker")
				framework.ExpectNoError(err)

				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.CleanupTempDir(initialDir, tempDir)
				})

				devsyUpCtx, cancel := context.WithDeadline(ctx, time.Now().Add(5*time.Minute))
				defer cancel()
				err = f.DevsyUp(devsyUpCtx, tempDir, "--ssh-tunnel-mode")
				framework.ExpectNoError(err)

				for i := range 3 {
					sshCtx, cancelSSH := context.WithDeadline(ctx, time.Now().Add(20*time.Second))
					out, err := f.DevsySSH(
						sshCtx,
						tempDir,
						"echo iteration-"+strings.Repeat("x", i),
					)
					cancelSSH()
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
				if runtime.GOOS == osWindows {
					ginkgo.Skip("skipping on windows")
				}

				tempDir, err := framework.CopyToTempDir("tests/ssh/testdata/local-test")
				framework.ExpectNoError(err)

				sshConfigDir := ginkgo.GinkgoT().TempDir()
				sshConfigPath := filepath.Join(sshConfigDir, "config")

				f := framework.NewDefaultFramework(initialDir + "/bin")
				_ = f.DevsyProviderAdd(ctx, "docker")
				err = f.DevsyProviderUse(ctx, "docker")
				framework.ExpectNoError(err)

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
