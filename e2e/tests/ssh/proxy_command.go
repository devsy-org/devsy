package ssh

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/secrets"
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
				framework.SkipUnlessOS("windows", "skipping on non-Windows")

				tempDir := framework.CopyTestdata("tests/ssh/testdata/local-test", initialDir)

				baseFramework := framework.NewDefaultFramework(initialDir + "/bin")
				sourcePath := filepath.Join(baseFramework.DevsyBinDir, baseFramework.DevsyBinName)
				fixtureDir := filepath.Join(ginkgo.GinkgoT().TempDir(), "Devsy Test")
				framework.ExpectNoError(os.MkdirAll(fixtureDir, 0o700))
				fixturePath := filepath.Join(fixtureDir, baseFramework.DevsyBinName)
				// #nosec G304 -- controlled path to the E2E fixture binary
				binary, err := os.ReadFile(sourcePath)
				framework.ExpectNoError(err)
				framework.ExpectNoError(os.WriteFile(fixturePath, binary, 0o600))
				gomega.Expect(fixturePath).To(gomega.ContainSubstring(" "))

				f, err := framework.SetupDockerProvider(fixtureDir, "podman")
				framework.ExpectNoError(err)

				sshConfigPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "ssh config")
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
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
				traceDir := ginkgo.GinkgoT().TempDir()
				traceRoot, err := os.OpenRoot(traceDir)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(traceRoot.Close)
				tracePath := filepath.Join(traceDir, proxyCommandTraceFile)
				traceFile, err := traceRoot.OpenFile(
					proxyCommandTraceFile,
					os.O_CREATE|os.O_EXCL|os.O_WRONLY,
					0o600,
				)
				framework.ExpectNoError(err)
				framework.ExpectNoError(traceFile.Close())
				sshCtx, cancelSSH := context.WithTimeout(ctx, 30*time.Second)
				defer cancelSSH()
				// #nosec G204 -- controlled OpenSSH invocation for the E2E test
				cmd := exec.CommandContext(
					sshCtx,
					sshPath,
					"-F", sshConfigPath,
					"-vvv", "-E", tracePath,
					"-o", "BatchMode=yes",
					host,
					"printf",
					"proxy-command-ok",
				)
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				started := time.Now()
				err = cmd.Run()
				elapsed := time.Since(started)
				sshErr, cause, parentErr := sshCtx.Err(), context.Cause(sshCtx), ctx.Err()
				exitCode := -1
				if cmd.ProcessState != nil {
					exitCode = cmd.ProcessState.ExitCode()
				}
				diagnostic := fmt.Sprintf(
					"started=%s elapsed=%s sshContext=%v cause=%v parentContext=%v exitCode=%d; %s",
					started.UTC().
						Format(time.RFC3339Nano),
					elapsed,
					sshErr,
					cause,
					parentErr,
					exitCode,
					proxyCommandOutputDiagnostic(traceRoot, stdout.String(), stderr.String()),
				)
				ginkgo.AddReportEntry(
					"OpenSSH ProxyCommand diagnostic",
					diagnostic,
					ginkgo.ReportEntryVisibilityFailureOrVerbose,
				)
				framework.ExpectNoError(err, "OpenSSH should launch ProxyCommand; %s", diagnostic)
				gomega.Expect(strings.TrimSpace(stdout.String())).To(
					gomega.Equal("proxy-command-ok"),
				)
			},
		)
	},
)

func proxyCommandOutputDiagnostic(traceRoot *os.Root, stdout, stderr string) string {
	trace, err := traceRoot.ReadFile(proxyCommandTraceFile)
	redactor := secrets.NewEnvironmentRedactor(os.Environ())
	return fmt.Sprintf(
		"stdout=%s stderr=%s traceReadError=%s trace=%s",
		proxyCommandExcerpt(redactor, stdout),
		proxyCommandExcerpt(redactor, stderr),
		proxyCommandExcerpt(
			redactor,
			fmt.Sprint(err),
		),
		proxyCommandExcerpt(redactor, string(trace)),
	)
}

const (
	proxyCommandTraceFile    = "ssh trace.log"
	proxyCommandExcerptLimit = 8192
)

func proxyCommandExcerpt(redactor *secrets.Redactor, text string) string {
	quoted := strconv.QuoteToASCII(redactor.Redact(text))
	if len(quoted) <= proxyCommandExcerptLimit {
		return quoted
	}
	return "[truncated]" + quoted[len(quoted)-proxyCommandExcerptLimit:]
}
