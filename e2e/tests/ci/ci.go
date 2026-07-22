package ci

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const ciCommand = "ci"

// expectWorkspaceGone lists workspaces (asserting the list itself succeeds) and
// verifies the given workspace is absent, so teardown failures aren't masked by
// a generic list error.
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

			// --workspace-env is persisted to /etc/envfile.json during setup;
			// assert it landed there rather than relying on shell env-loading,
			// which varies between login and non-login (docker exec) shells.
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
			// The postCreateCommand in this testdata writes $MY_SECRET to a file;
			// secrets are injected into lifecycle commands, not the run command's
			// own environment, so the run command reads it back from the file.
			tempDir, f := setupCIFrom(initialDir, "tests/ci/testdata/secrets")

			secretsFile := filepath.Join(tempDir, "ci.secrets")
			err := os.WriteFile(secretsFile, []byte("MY_SECRET=s3cr3t\n"), 0o600)
			framework.ExpectNoError(err)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				ciCommand, tempDir,
				"--secrets-file", secretsFile,
				"--", "cat", "/tmp/ci-secret.txt",
			})
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(stdout)).To(gomega.ContainSubstring("s3cr3t"))
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
	tempDir, err := framework.CopyToTempDir(testdataPath)
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

	f, err := framework.SetupDockerProvider(initialDir+"/bin", "docker")
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

	return tempDir, f
}
