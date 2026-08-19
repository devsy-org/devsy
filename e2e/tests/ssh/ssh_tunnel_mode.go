package ssh

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const tunnelActiveMarker = "waiting for shutdown signal"

const tunnelActiveTimeout = 4 * time.Minute

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type tunnelUpProcess struct {
	cmd     *exec.Cmd
	output  *safeBuffer
	exited  chan struct{}
	exitErr error
}

func startTunnelUp(
	f *framework.Framework, workspace string, extraArgs ...string,
) (*tunnelUpProcess, error) {
	args := []string{
		cmdWorkspace, "up",
		names.Flag(names.Debug),
		names.Flag(names.IDE), "none",
		names.Flag(names.SSHTunnel),
	}
	args = append(args, extraArgs...)
	args = append(args, workspace)

	// #nosec G204 -- test binary with controlled arguments
	cmd := exec.Command(filepath.Join(f.DevsyBinDir, f.DevsyBinName), args...)
	out := &safeBuffer{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start devsy up --ssh-tunnel: %w", err)
	}

	p := &tunnelUpProcess{cmd: cmd, output: out, exited: make(chan struct{})}
	go func() {
		p.exitErr = cmd.Wait()
		close(p.exited)
	}()
	return p, nil
}

func (p *tunnelUpProcess) waitUntilActive(ctx context.Context) {
	gomega.Eventually(func() (string, error) {
		out := p.output.String()
		select {
		case <-p.exited:
			return out, gomega.StopTrying(
				"devsy up exited before the tunnel became active",
			).Wrap(p.exitErr)
		default:
			return out, nil
		}
	}).WithContext(ctx).WithTimeout(tunnelActiveTimeout).WithPolling(100 * time.Millisecond).
		Should(gomega.ContainSubstring(tunnelActiveMarker))
}

func (p *tunnelUpProcess) stop() {
	select {
	case <-p.exited:
		return
	default:
	}
	_ = p.cmd.Process.Signal(syscall.SIGINT)
	select {
	case <-p.exited:
	case <-time.After(15 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.exited
	}
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

				proc, err := startTunnelUp(f, tempDir)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(proc.stop)

				proc.waitUntilActive(ctx)

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

				proc, err := startTunnelUp(f, tempDir, "--ssh-config", sshConfigPath)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(proc.stop)

				proc.waitUntilActive(ctx)

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

				proc, err := startTunnelUp(f, tempDir, "--ssh-config", sshConfigPath)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(proc.stop)

				proc.waitUntilActive(ctx)

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

				proc, err := startTunnelUp(f, tempDir)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(proc.stop)

				proc.waitUntilActive(ctx)

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
