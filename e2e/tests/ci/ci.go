package ci

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	ciCommand             = "ci"
	secretCmd             = "secret"
	ciSecretName          = "MY_SECRET"
	noStatusFlag          = "--no-status"
	resultFormatPlainFlag = "--result-format=plain"
)

func expectWorkspaceGone(ctx context.Context, f *framework.Framework, name string) {
	list, err := f.DevsyListParsed(ctx)
	framework.ExpectNoError(err)
	wantID := workspace.ToID(name)
	for _, w := range list {
		gomega.Expect(w.ID).ToNot(gomega.Equal(wantID),
			"workspace %s should be torn down", wantID)
	}
}

var _ = ginkgo.Describe("devsy ci test suite", ginkgo.Label("ci"), ginkgo.Ordered, func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.It("should build, run a command, and tear the workspace down",
		func(ctx context.Context) {
			tempDir, f := setupCI(initialDir)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				ciCommand, tempDir,
				"--", "sh", "-c", "echo -n hello-ci",
			})
			framework.ExpectNoError(err)
			gomega.Expect(stdout).To(gomega.ContainSubstring("hello-ci"))

			expectWorkspaceGone(ctx, f, tempDir)
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should propagate a non-zero exit code from the run command",
		func(ctx context.Context) {
			tempDir, f := setupCI(initialDir)

			_, _, err := f.ExecCommandCapture(ctx, []string{
				ciCommand, tempDir,
				"--", "sh", "-c", "exit 3",
			})
			var exitErr *exec.ExitError
			gomega.Expect(errors.As(err, &exitErr)).To(gomega.BeTrue(),
				"expected an exec.ExitError, got %v", err)
			gomega.Expect(exitErr.ExitCode()).To(gomega.Equal(3))

			expectWorkspaceGone(ctx, f, tempDir)
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.DescribeTable(
		"should preserve child streams and control progress visibility",
		func(ctx context.Context, flags []string, wantStatus, wantDebug bool) {
			tempDir, f := setupCI(initialDir)
			args := []string{ciCommand}
			args = append(args, flags...)
			args = append(args, tempDir, "--", "sh", "-c",
				"printf 'child-out:[RUN] {\"kind\":\"status\"}'; printf 'child-err' >&2")
			stdout, stderr, err := f.ExecCommandCapture(ctx, args)
			framework.ExpectNoError(err)
			gomega.Expect(stdout).To(gomega.Equal(`child-out:[RUN] {"kind":"status"}`))
			gomega.Expect(stderr).To(gomega.ContainSubstring("child-err"))
			if wantStatus {
				gomega.Expect(stderr).To(gomega.ContainSubstring("[RUN]"))
			} else {
				gomega.Expect(stderr).NotTo(gomega.ContainSubstring("[RUN]"))
				gomega.Expect(stderr).NotTo(gomega.ContainSubstring("[OK]"))
			}
			if len(flags) > 0 && flags[0] == noStatusFlag && len(flags) == 2 {
				gomega.Expect(stderr).To(gomega.Equal("child-err"))
			}
			if wantDebug {
				gomega.Expect(stderr).To(gomega.ContainSubstring("running command in devcontainer"))
			}
			expectWorkspaceGone(ctx, f, tempDir)
		},
		ginkgo.Entry(
			"default",
			[]string{},
			true,
			false,
			ginkgo.SpecTimeout(framework.TimeoutShort()),
		),
		ginkgo.Entry("disabled", []string{noStatusFlag, resultFormatPlainFlag}, false, false,
			ginkgo.SpecTimeout(framework.TimeoutShort())),
		ginkgo.Entry(
			"quiet",
			[]string{noStatusFlag, "--quiet", resultFormatPlainFlag},
			false,
			false,
			ginkgo.SpecTimeout(framework.TimeoutShort()),
		),
		ginkgo.Entry(
			"warn level",
			[]string{noStatusFlag, "--log-level=warn", resultFormatPlainFlag}, false, false,
			ginkgo.SpecTimeout(framework.TimeoutShort())),
		ginkgo.Entry(
			"debug",
			[]string{
				noStatusFlag,
				"--debug",
				"--log-output=json",
				resultFormatPlainFlag,
			},
			false,
			true,
			ginkgo.SpecTimeout(framework.TimeoutShort()),
		),
		ginkgo.Entry(
			"explicit false",
			[]string{"--no-status=false", resultFormatPlainFlag},
			true,
			false,
			ginkgo.SpecTimeout(framework.TimeoutShort()),
		),
	)

	ginkgo.DescribeTable("should preserve a real warning while suppressing progress",
		func(ctx context.Context, flags []string, wantWarning bool) {
			tempDir, f := setupCI(initialDir)
			configPath := filepath.Join(tempDir, ".devcontainer", "devcontainer.json")
			framework.ExpectNoError(os.WriteFile(configPath, []byte(`{
  "name": "CI warning test",
  "image": "ghcr.io/devsy-org/test-images/base:ubuntu",
  "workspaceFolder": "/workspaces/ci"
}`), 0o600))
			args := []string{ciCommand, noStatusFlag}
			args = append(args, flags...)
			args = append(args, resultFormatPlainFlag, tempDir,
				"--", "sh", "-c", "printf 'warning-child-out'; printf 'warning-child-err' >&2")
			stdout, stderr, err := f.ExecCommandCapture(ctx, args)
			framework.ExpectNoError(err)
			gomega.Expect(stdout).To(gomega.Equal("warning-child-out"))
			gomega.Expect(stderr).To(gomega.ContainSubstring("warning-child-err"))
			if wantWarning {
				gomega.Expect(stderr).
					To(gomega.ContainSubstring("sets workspaceFolder without workspaceMount"))
			} else {
				gomega.Expect(stderr).NotTo(gomega.ContainSubstring(
					"sets workspaceFolder without workspaceMount",
				))
			}
			gomega.Expect(stderr).NotTo(gomega.ContainSubstring("[RUN]"))
			expectWorkspaceGone(ctx, f, tempDir)
		},
		ginkgo.Entry("warning level", []string{"--log-level=warn"}, true,
			ginkgo.SpecTimeout(framework.TimeoutShort())),
		ginkgo.Entry("quiet", []string{"--quiet"}, false,
			ginkgo.SpecTimeout(framework.TimeoutShort())),
	)

	ginkgo.It("should retain public JSON result output without status events",
		func(ctx context.Context) {
			tempDir, f := setupCI(initialDir)
			stdout, stderr, err := f.ExecCommandCapture(ctx, []string{
				ciCommand, noStatusFlag, "--result-format=json", tempDir,
				"--", "sh", "-c", "printf 'json-child'",
			})
			framework.ExpectNoError(err)
			gomega.Expect(stdout).To(gomega.Equal("json-child"))
			gomega.Expect(stderr).To(gomega.ContainSubstring(`"kind":"result"`))
			gomega.Expect(stderr).NotTo(gomega.ContainSubstring(`"kind":"status"`))
			expectWorkspaceGone(ctx, f, tempDir)
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It(
		"should preserve child stderr and cleanup when the child exits non-zero with status disabled",
		func(ctx context.Context) {
			tempDir, f := setupCI(initialDir)
			_, stderr, err := f.ExecCommandCapture(ctx, []string{
				ciCommand, noStatusFlag, resultFormatPlainFlag, tempDir,
				"--", "sh", "-c", "printf 'child-failed' >&2; exit 7",
			})
			var exitErr *exec.ExitError
			gomega.Expect(errors.As(err, &exitErr)).To(gomega.BeTrue(), "got %v", err)
			gomega.Expect(exitErr.ExitCode()).To(gomega.Equal(7))
			gomega.Expect(stderr).To(gomega.ContainSubstring("child-failed"))
			gomega.Expect(stderr).NotTo(gomega.ContainSubstring("[FAIL]"))
			expectWorkspaceGone(ctx, f, tempDir)
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)

	ginkgo.It(
		"should emit a machine error envelope and preserve child exit status with status disabled",
		func(ctx context.Context) {
			tempDir, f := setupCI(initialDir)
			_, stderr, err := f.ExecCommandCapture(ctx, []string{
				ciCommand, noStatusFlag, "--log-output=json", resultFormatPlainFlag, tempDir,
				"--", "sh", "-c", "printf 'machine-child-error' >&2; exit 7",
			})
			var exitErr *exec.ExitError
			gomega.Expect(errors.As(err, &exitErr)).To(gomega.BeTrue(), "got %v", err)
			gomega.Expect(exitErr.ExitCode()).To(gomega.Equal(7))
			gomega.Expect(stderr).To(gomega.ContainSubstring("machine-child-error"))
			gomega.Expect(stderr).To(gomega.ContainSubstring(`"kind":"error"`))
			gomega.Expect(stderr).NotTo(gomega.ContainSubstring(`"kind":"status"`))
			expectWorkspaceGone(ctx, f, tempDir)
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should retain build failure diagnostics and cleanup with status disabled",
		func(ctx context.Context) {
			tempDir, f := setupCIFrom(initialDir, "tests/up/testdata/docker-recovery")
			_, stderr, err := f.ExecCommandCapture(ctx, []string{
				ciCommand, noStatusFlag, resultFormatPlainFlag, tempDir,
				"--", "sh", "-c", "exit 0",
			})
			gomega.Expect(err).To(gomega.HaveOccurred())
			gomega.Expect(stderr).To(gomega.ContainSubstring("intentional build failure"))
			gomega.Expect(stderr).NotTo(gomega.ContainSubstring("[FAIL]"))
			expectWorkspaceGone(ctx, f, tempDir)
		}, ginkgo.SpecTimeout(framework.TimeoutLong()))

	ginkgo.It("should cancel the running command and cleanup with status disabled",
		func(ctx context.Context) {
			tempDir, f := setupCI(initialDir)
			binaryPath := filepath.Join(f.DevsyBinDir, f.DevsyBinName)
			cmd := &exec.Cmd{
				Path: binaryPath,
				Args: []string{
					binaryPath, ciCommand, noStatusFlag, resultFormatPlainFlag, tempDir,
					"--", "sh", "-c", "printf 'ci-ready\\n'; sleep 120",
				},
				WaitDelay: 5 * time.Second,
			}
			stdout, err := cmd.StdoutPipe()
			framework.ExpectNoError(err)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			framework.ExpectNoError(cmd.Start())
			ready := make(chan struct{}, 1)
			go func() {
				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					if scanner.Text() == "ci-ready" {
						select {
						case ready <- struct{}{}:
						default:
						}
					}
				}
			}()
			ginkgo.DeferCleanup(func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Signal(os.Interrupt)
					_, _ = waitCommandBounded(cmd)
				}
			})

			select {
			case <-ready:
			case <-ctx.Done():
				ginkgo.Fail("timed out waiting for the CI child readiness marker")
			}

			framework.ExpectNoError(cmd.Process.Signal(os.Interrupt))
			var timedOut bool
			err, timedOut = waitCommandBounded(cmd)
			gomega.Expect(timedOut).To(gomega.BeFalse(), "CI command did not stop after interrupt")
			gomega.Expect(err).To(gomega.HaveOccurred())
			gomega.Expect(stderr.String()).NotTo(gomega.ContainSubstring("[RUN]"))
			gomega.Expect(stderr.String()).NotTo(gomega.ContainSubstring("[FAIL]"))
			expectWorkspaceGone(ctx, f, tempDir)
		}, ginkgo.SpecTimeout(framework.TimeoutLong()))

	ginkgo.It("should retain the workspace when --no-status and --keep are passed",
		func(ctx context.Context) {
			tempDir, f := setupCI(initialDir)
			ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				ciCommand, noStatusFlag, resultFormatPlainFlag, tempDir,
				"--keep", "--", "sh", "-c", "printf kept",
			})
			framework.ExpectNoError(err)
			gomega.Expect(stdout).To(gomega.Equal("kept"))

			_, err = f.FindWorkspace(ctx, tempDir)
			framework.ExpectNoError(err)
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should retain the workspace when --keep is passed",
		func(ctx context.Context) {
			tempDir, f := setupCI(initialDir)
			ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				ciCommand, tempDir, "--keep",
				"--", "sh", "-c", "echo -n kept",
			})
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(stdout)).To(gomega.ContainSubstring("kept"))

			_, err = f.FindWorkspace(ctx, tempDir)
			framework.ExpectNoError(err)
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should pass --remote-env to the run command",
		func(ctx context.Context) {
			tempDir, f := setupCI(initialDir)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				ciCommand, tempDir,
				"--remote-env", "MY_CI_VAR=ci_value",
				"--", "sh", "-c", "echo -n $MY_CI_VAR",
			})
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(stdout)).To(gomega.ContainSubstring("ci_value"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should pass --workspace-env into the container",
		func(ctx context.Context) {
			tempDir, f := setupCI(initialDir)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				ciCommand, tempDir,
				"--workspace-env", "WS_CI_VAR=ws_value",
				"--", "cat", "/etc/envfile.json",
			})
			framework.ExpectNoError(err)
			gomega.Expect(stdout).To(gomega.ContainSubstring(`"WS_CI_VAR":"ws_value"`))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should accept the run command via --run-cmd",
		func(ctx context.Context) {
			tempDir, f := setupCI(initialDir)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				ciCommand, tempDir,
				"--run-cmd", "echo -n from-run-cmd",
			})
			framework.ExpectNoError(err)
			gomega.Expect(stdout).To(gomega.ContainSubstring("from-run-cmd"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should inject secrets from --secrets-file into lifecycle commands",
		func(ctx context.Context) {
			tempDir, f := setupCIFrom(initialDir, "tests/ci/testdata/secrets")

			secretsFile := filepath.Join(tempDir, "ci.secrets.json")
			err := os.WriteFile(secretsFile, []byte(`{"MY_SECRET":"s3cr3t"}`), 0o600)
			framework.ExpectNoError(err)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				ciCommand, tempDir,
				"--secrets-file", secretsFile,
				"--", "cat", "/tmp/ci-secret.txt",
			})
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(stdout)).To(gomega.ContainSubstring("s3cr3t"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should inject a managed secret via --secret into lifecycle commands",
		func(ctx context.Context) {
			framework.UseFileSecretsBackend()
			tempDir, f := setupCIFrom(initialDir, "tests/ci/testdata/secrets")

			_, err := f.ExecCommandOutput(ctx,
				[]string{secretCmd, "set", ciSecretName, "--value", "managed-ci-42"})
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() {
				_, _ = f.ExecCommandOutput(context.Background(),
					[]string{secretCmd, "delete", ciSecretName})
			})

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				ciCommand, tempDir,
				"--secret", ciSecretName,
				"--", "cat", "/tmp/ci-secret.txt",
			})
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(stdout)).To(gomega.ContainSubstring("managed-ci-42"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should accept --cache-from and complete the run",
		func(ctx context.Context) {
			tempDir, f := setupCI(initialDir)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				ciCommand, tempDir,
				"--cache-from", "ghcr.io/devsy-org/test-images/base:ubuntu",
				"--", "sh", "-c", "echo -n cached-run",
			})
			framework.ExpectNoError(err)
			gomega.Expect(stdout).To(gomega.ContainSubstring("cached-run"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))
})

func setupCI(initialDir string) (string, *framework.Framework) {
	return setupCIFrom(initialDir, "tests/ci/testdata/ci")
}

func setupCIFrom(initialDir, testdataPath string) (string, *framework.Framework) {
	tempDir := framework.CopyTestdata(testdataPath, initialDir)

	f, err := framework.SetupDockerProvider(initialDir+"/bin", "docker")
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

	return tempDir, f
}

func waitCommandBounded(cmd *exec.Cmd) (error, bool) {
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	grace := time.NewTimer(15 * time.Second)
	defer grace.Stop()
	select {
	case err := <-done:
		return err, false
	case <-grace.C:
		if cmd.Cancel != nil {
			_ = cmd.Cancel()
		} else if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}

	forced := time.NewTimer(10 * time.Second)
	defer forced.Stop()
	select {
	case err := <-done:
		return err, true
	case <-forced.C:
		return errors.New("CI command did not exit after forced termination"), true
	}
}
