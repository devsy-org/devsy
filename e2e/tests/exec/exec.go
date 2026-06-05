package exec

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	cmdWorkspace        = "workspace"
	execCommand         = "exec"
	workspaceFolderFlag = "--workspace-folder"
	echoCommand         = "echo"
)

var _ = ginkgo.Describe("devsy exec test suite", ginkgo.Label("exec"), ginkgo.Ordered, func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.It("should exec a command in a running workspace container",
		func(ctx context.Context) {
			tempDir, f, err := setupWorkspaceAndUp(ctx, "tests/exec/testdata/exec", initialDir)
			framework.ExpectNoError(err)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdWorkspace, execCommand,
				workspaceFolderFlag, tempDir,
				"--", echoCommand, "-n", "hello",
			})
			framework.ExpectNoError(err)
			gomega.Expect(stdout).To(gomega.Equal("hello"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should pass remote-env to the container",
		func(ctx context.Context) {
			tempDir, f, err := setupWorkspaceAndUp(
				ctx, "tests/exec/testdata/remote-env", initialDir,
			)
			framework.ExpectNoError(err)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdWorkspace, execCommand,
				workspaceFolderFlag, tempDir,
				"--remote-env", "MY_TEST_VAR=test_value",
				"--", "sh", "-c", "echo -n $MY_TEST_VAR",
			})
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(stdout)).To(gomega.Equal("test_value"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should run commands in the workspace directory",
		func(ctx context.Context) {
			tempDir, f, err := setupWorkspaceAndUp(
				ctx, "tests/exec/testdata/remote-env", initialDir,
			)
			framework.ExpectNoError(err)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdWorkspace, execCommand,
				workspaceFolderFlag, tempDir,
				"--", "pwd",
			})
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(stdout)).To(gomega.HavePrefix("/workspaces/"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should run commands as the remote user",
		func(ctx context.Context) {
			tempDir, f, err := setupWorkspaceAndUp(
				ctx, "tests/exec/testdata/remote-env", initialDir,
			)
			framework.ExpectNoError(err)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdWorkspace, execCommand,
				workspaceFolderFlag, tempDir,
				"--", "whoami",
			})
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(stdout)).To(gomega.Equal("vscode"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should inject remoteEnv from devcontainer config",
		func(ctx context.Context) {
			tempDir, f, err := setupWorkspaceAndUp(
				ctx, "tests/exec/testdata/remote-env", initialDir,
			)
			framework.ExpectNoError(err)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdWorkspace, execCommand,
				workspaceFolderFlag, tempDir,
				"--", "sh", "-c", "echo -n $CONFIG_VAR",
			})
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(stdout)).To(gomega.Equal("from_config"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should let CLI remote-env override config remoteEnv",
		func(ctx context.Context) {
			tempDir, f, err := setupWorkspaceAndUp(
				ctx, "tests/exec/testdata/remote-env", initialDir,
			)
			framework.ExpectNoError(err)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdWorkspace, execCommand,
				workspaceFolderFlag, tempDir,
				"--remote-env", "CONFIG_VAR=from_cli",
				"--", "sh", "-c", "echo -n $CONFIG_VAR",
			})
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(stdout)).To(gomega.Equal("from_cli"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should probe user environment and include PATH",
		func(ctx context.Context) {
			tempDir, f, err := setupWorkspaceAndUp(ctx, "tests/exec/testdata/envprobe", initialDir)
			framework.ExpectNoError(err)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdWorkspace, execCommand,
				workspaceFolderFlag, tempDir,
				"--", "sh", "-c", "echo -n $PATH",
			})
			framework.ExpectNoError(err)
			gomega.Expect(stdout).To(gomega.ContainSubstring("/usr/local/bin"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should skip env probe when --default-user-env-probe is none",
		func(ctx context.Context) {
			tempDir, f, err := setupWorkspaceAndUp(ctx, "tests/exec/testdata/envprobe", initialDir)
			framework.ExpectNoError(err)

			_, _, err = f.ExecCommandCapture(ctx, []string{
				cmdWorkspace, execCommand,
				workspaceFolderFlag, tempDir,
				"--default-user-env-probe", "none",
				"--", echoCommand, "-n", "ok",
			})
			framework.ExpectNoError(err)
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should exec by workspace name",
		func(ctx context.Context) {
			tempDir, f, err := setupWorkspaceAndUp(ctx, "tests/exec/testdata/exec", initialDir)
			framework.ExpectNoError(err)

			wsName := workspace.ToID(tempDir)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdWorkspace, execCommand,
				wsName,
				"--", echoCommand, "-n", "hello",
			})
			framework.ExpectNoError(err)
			gomega.Expect(stdout).To(gomega.Equal("hello"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should error when both name and --workspace-folder are given",
		func(ctx context.Context) {
			tempDir, f, err := setupWorkspaceAndUp(ctx, "tests/exec/testdata/exec", initialDir)
			framework.ExpectNoError(err)

			wsName := workspace.ToID(tempDir)

			_, _, err = f.ExecCommandCapture(ctx, []string{
				cmdWorkspace, execCommand,
				wsName,
				workspaceFolderFlag, tempDir,
				"--", echoCommand, "-n", "hello",
			})
			framework.ExpectError(err)
			gomega.Expect(err.Error()).To(gomega.ContainSubstring(
				"specify either a workspace name or --workspace-folder/--container-id, not both",
			))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should find container by custom id-label",
		func(ctx context.Context) {
			tempDir, f, err := setupWorkspace("tests/exec/testdata/exec", initialDir)
			framework.ExpectNoError(err)

			err = f.DevsyUp(ctx, tempDir,
				"--id-label", "devsy.exec.test=idlabel")
			framework.ExpectNoError(err)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdWorkspace, execCommand,
				workspaceFolderFlag, tempDir,
				"--id-label", "devsy.exec.test=idlabel",
				"--", echoCommand, "-n", "found",
			})
			framework.ExpectNoError(err)
			gomega.Expect(stdout).To(gomega.Equal("found"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should emit JSON envelope on stderr with --result-format json",
		func(ctx context.Context) {
			tempDir, f, err := setupWorkspaceAndUp(ctx, "tests/exec/testdata/exec", initialDir)
			framework.ExpectNoError(err)

			stdout, stderr, err := f.ExecCommandCapture(ctx, []string{
				cmdWorkspace, execCommand,
				"--result-format", "json",
				workspaceFolderFlag, tempDir,
				"--", echoCommand, "-n", "hello",
			})
			framework.ExpectNoError(err)
			gomega.Expect(stdout).To(gomega.Equal("hello"))

			lines := strings.Split(strings.TrimSpace(stderr), "\n")
			gomega.Expect(lines).NotTo(gomega.BeEmpty())
			lastLine := lines[len(lines)-1]
			var envelope config.ResultEnvelope
			err = json.Unmarshal([]byte(lastLine), &envelope)
			framework.ExpectNoError(err)
			gomega.Expect(envelope.Outcome).To(gomega.Equal("success"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should suppress JSON envelope on stderr with --result-format plain",
		func(ctx context.Context) {
			tempDir, f, err := setupWorkspaceAndUp(ctx, "tests/exec/testdata/exec", initialDir)
			framework.ExpectNoError(err)

			stdout, stderr, err := f.ExecCommandCapture(ctx, []string{
				cmdWorkspace, execCommand,
				"--result-format", "plain",
				workspaceFolderFlag, tempDir,
				"--", echoCommand, "-n", "hello",
			})
			framework.ExpectNoError(err)
			gomega.Expect(stdout).To(gomega.Equal("hello"))

			for line := range strings.SplitSeq(stderr, "\n") {
				var envelope config.ResultEnvelope
				if json.Unmarshal([]byte(line), &envelope) == nil {
					gomega.Expect(envelope.Outcome).To(gomega.BeEmpty(),
						"expected no JSON envelope on stderr, but found one: %s", line)
				}
			}
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))
})
