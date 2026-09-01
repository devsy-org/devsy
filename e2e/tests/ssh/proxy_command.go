package ssh

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"devsy Windows SSH ProxyCommand",
	ginkgo.Label("ssh-proxy-command"),
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It(
			"should launch a workspace through an executable path containing spaces",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				if runtime.GOOS != osWindows {
					ginkgo.Skip("skipping on non-Windows")
				}

				tempDir, err := framework.CopyToTempDir("tests/ssh/testdata/local-test")
				framework.ExpectNoError(err)

				baseFramework := framework.NewDefaultFramework(initialDir + "/bin")
				sourcePath := filepath.Join(baseFramework.DevsyBinDir, baseFramework.DevsyBinName)
				fixtureDir := filepath.Join(ginkgo.GinkgoT().TempDir(), "Devsy Test")
				framework.ExpectNoError(os.MkdirAll(fixtureDir, 0o700))
				fixturePath := filepath.Join(fixtureDir, baseFramework.DevsyBinName)
				binary, err := os.ReadFile(sourcePath)
				framework.ExpectNoError(err)
				framework.ExpectNoError(os.WriteFile(fixturePath, binary, 0o700))
				gomega.Expect(fixturePath).To(gomega.ContainSubstring(" "))

				f := framework.NewDefaultFramework(fixtureDir)
				_ = f.DevsyProviderAdd(ctx, "docker")
				err = f.DevsyProviderUse(ctx, "docker")
				framework.ExpectNoError(err)

				sshConfigPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "ssh config")
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.CleanupTempDir(initialDir, tempDir)
				})

				upCtx, cancelUp := context.WithTimeout(ctx, 5*time.Minute)
				defer cancelUp()
				err = f.DevsyUp(upCtx, tempDir, "--ssh-config", sshConfigPath)
				framework.ExpectNoError(err)

				configBytes, err := os.ReadFile(filepath.Clean(sshConfigPath))
				framework.ExpectNoError(err)
				config := string(configBytes)
				expectedPath := strings.ReplaceAll(fixturePath, `\`, "/")
				gomega.Expect(config).To(
					gomega.ContainSubstring(`ProxyCommand "`+expectedPath+`"`),
					"SSH config should use forward slashes for the executable path",
				)
				gomega.Expect(config).NotTo(
					gomega.ContainSubstring(fixturePath),
					"SSH config should not contain the native Windows executable path",
				)

				sshPath, err := exec.LookPath("ssh.exe")
				framework.ExpectNoError(err)
				host := filepath.Base(tempDir) + ".devsy"
				sshCtx, cancelSSH := context.WithTimeout(ctx, 30*time.Second)
				defer cancelSSH()
				cmd := exec.CommandContext(
					sshCtx,
					sshPath,
					"-F", sshConfigPath,
					"-o", "BatchMode=yes",
					host,
					"printf",
					"proxy-command-ok",
				)
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				err = cmd.Run()
				framework.ExpectNoError(
					err,
					"OpenSSH should launch ProxyCommand; stdout=%q stderr=%q",
					stdout.String(), stderr.String(),
				)
				gomega.Expect(strings.TrimSpace(stdout.String())).To(gomega.Equal("proxy-command-ok"))
			},
		)
	},
)
