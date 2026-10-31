package up

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/devsy-org/devsy/e2e/framework"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	docker "github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/language"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/workspacejournal"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	noStatusFlag     = "--no-status"
	resultFormatFlag = "--result-format"
	debugFlag        = "--debug"
	eventsCommand    = "events"
	ideNone          = "none"
)

var _ = ginkgo.Describe("testing up command", ginkgo.Label("up-workspaces"), func() {
	var dockerHelper *docker.DockerHelper
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)

		dockerHelper = &docker.DockerHelper{DockerCommand: dockerValue}
	})

	ginkgo.It("with env vars", func(ctx context.Context) {
		f, err := framework.SetupDockerProvider(filepath.Join(initialDir, "bin"), dockerValue)
		framework.ExpectNoError(err)

		name := "vscode-remote-try-python"
		ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, name)

		// Wait for devsy workspace to come online (deadline: 30s)
		err = f.DevsyUp(ctx, "https://github.com/microsoft/vscode-remote-try-python.git")
		framework.ExpectNoError(err)

		// check env var
		out, err := f.DevsySSH(ctx, name, "echo -n $TEST_VAR")
		framework.ExpectNoError(err)
		framework.ExpectEqual(out, "", "should be empty")

		// set env var
		value := "test-variable"
		err = f.DevsyUp(ctx, name, "--workspace-env", "TEST_VAR="+value)
		framework.ExpectNoError(err)

		// check env var
		out, err = f.DevsySSH(ctx, name, "echo -n $TEST_VAR")
		framework.ExpectNoError(err)
		framework.ExpectEqual(out, value, "should be set now")

		// check env var again
		err = f.DevsyUp(ctx, name)
		framework.ExpectNoError(err)

		// check env var
		out, err = f.DevsySSH(ctx, name, "echo -n $TEST_VAR")
		framework.ExpectNoError(err)
		framework.ExpectEqual(out, value, "should still be set")

		// delete env var
		err = f.DevsyUp(ctx, name, "--workspace-env", "TEST_VAR=")
		framework.ExpectNoError(err)

		// check env var
		out, err = f.DevsySSH(ctx, name, "echo -n $TEST_VAR")
		framework.ExpectNoError(err)
		framework.ExpectEqual(out, "", "should be empty")

		// set env vars with file
		tmpDir, err := framework.CreateTempDir()
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tmpDir) })

		// create invalid env file
		invalidData := []byte("TEST VAR=" + value)
		workspaceEnvFileInvalid := filepath.Join(tmpDir, ".invalid")
		err = os.WriteFile(
			workspaceEnvFileInvalid,
			invalidData, 0o644)
		framework.ExpectNoError(err)

		// set env var
		err = f.DevsyUp(ctx, name, "--workspace-env-file", workspaceEnvFileInvalid)
		framework.ExpectError(err)

		// create valid env file
		validData := []byte("TEST_VAR=" + value)
		workspaceEnvFileValid := filepath.Join(tmpDir, ".valid")
		err = os.WriteFile(
			workspaceEnvFileValid,
			validData, 0o644)
		framework.ExpectNoError(err)

		// set env var
		err = f.DevsyUp(ctx, name, "--workspace-env-file", workspaceEnvFileValid)
		framework.ExpectNoError(err)

		// check env var
		out, err = f.DevsySSH(ctx, name, "echo -n $TEST_VAR")
		framework.ExpectNoError(err)
		framework.ExpectEqual(out, value, "should be set now")

		// delete env var
		err = f.DevsyUp(ctx, name, "--workspace-env", "TEST_VAR=")
		framework.ExpectNoError(err)

		// check env var
		out, err = f.DevsySSH(ctx, name, "echo -n $TEST_VAR")
		framework.ExpectNoError(err)
		framework.ExpectEqual(out, "", "should be empty")

		// create a second valid env file with a different env var
		validData = []byte("TEST_OTHER_VAR=" + value)
		workspaceEnvFileValid2 := filepath.Join(tmpDir, ".valid2")
		err = os.WriteFile(
			workspaceEnvFileValid2,
			validData, 0o644)
		framework.ExpectNoError(err)

		// set env var from both files
		err = f.DevsyUp(
			ctx,
			name,
			"--workspace-env-file",
			fmt.Sprintf("%s,%s", workspaceEnvFileValid, workspaceEnvFileValid2),
		)
		framework.ExpectNoError(err)

		// check env var from .valid file
		out, err = f.DevsySSH(ctx, name, "echo -n $TEST_VAR")
		framework.ExpectNoError(err)
		framework.ExpectEqual(out, value, "should be set now")

		// check env var from .valid2 file
		out, err = f.DevsySSH(ctx, name, "echo -n $TEST_OTHER_VAR")
		framework.ExpectNoError(err)
		framework.ExpectEqual(out, value, "should be set now")
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("suppresses up progress while retaining workspace events",
		func(ctx context.Context) {
			f, err := framework.SetupDockerProvider(filepath.Join(initialDir, "bin"), dockerValue)
			framework.ExpectNoError(err)

			tempDir, err := framework.CopyToTempDir("tests/up/testdata/no-devcontainer")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
			ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

			stdout, stderr, err := f.ExecCommandCapture(ctx, []string{
				cmdWorkspace, "up", noStatusFlag, resultFormatFlag + "=plain", debugFlag,
				flagIDEOption, ideNone, tempDir,
			})
			framework.ExpectNoError(err)
			gomega.Expect(stdout).NotTo(gomega.ContainSubstring("[RUN]"))
			gomega.Expect(stdout).NotTo(gomega.ContainSubstring("[OK]"))
			gomega.Expect(stderr).To(gomega.ContainSubstring("resolving workspace with ide="))

			workspace, err := f.FindWorkspace(ctx, tempDir)
			framework.ExpectNoError(err)
			eventsOutput, err := f.ExecCommandOutput(ctx, []string{
				cmdWorkspace, eventsCommand, resultFormatFlag, output.ModeJSON, workspace.ID,
			})
			framework.ExpectNoError(err)
			var eventEnvelope struct {
				SchemaVersion int                      `json:"schemaVersion"`
				Events        []workspacejournal.Event `json:"events"`
			}
			framework.ExpectNoError(json.Unmarshal([]byte(eventsOutput), &eventEnvelope))
			gomega.Expect(eventEnvelope.Events).NotTo(gomega.BeEmpty())
			gomega.Expect(eventEnvelope.Events[len(eventEnvelope.Events)-1]).To(gomega.SatisfyAll(
				gomega.HaveField("WorkspaceID", workspace.ID),
				gomega.HaveField("Pipeline", status.PipelineWorkspaceUp),
				gomega.HaveField("State", status.StateSucceeded),
			))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("create workspace without devcontainer.json", func(ctx context.Context) {
		const (
			providerName = "test-docker"
			remoteUser   = "vscode"
		)

		f := framework.NewDefaultFramework(initialDir + "/bin")
		tempDir, err := framework.CopyToTempDir("tests/up/testdata/no-devcontainer")
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

		// provider add, use and delete afterwards
		err = f.DevsyProviderAdd(ctx, dockerValue, "--name", providerName)
		framework.ExpectNoError(err)
		err = f.DevsyProviderUse(ctx, providerName)
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
			err := f.DevsyProviderDelete(cleanupCtx, providerName)
			framework.ExpectNoError(err)
		})

		err = f.DevsyUp(ctx, tempDir)
		framework.ExpectNoError(err)

		workspace, err := f.FindWorkspace(ctx, tempDir)
		framework.ExpectNoError(err)

		infoOutput, err := f.DevsySSH(
			ctx,
			workspace.ID,
			"devsy container info --result-format json",
		)
		framework.ExpectNoError(err)
		sshHost := workspace.ID + pkgconfig.SSHHostSuffix
		var info struct {
			SSHCommand string `json:"sshCommand"`
			SSHHost    string `json:"sshHost"`
			RemoteUser string `json:"remoteUser"`
		}
		framework.ExpectNoError(json.Unmarshal([]byte(infoOutput), &info))
		gomega.Expect(info.SSHHost).To(gomega.Equal(sshHost))
		gomega.Expect(info.RemoteUser).To(gomega.Equal(remoteUser))
		gomega.Expect(info.SSHCommand).To(gomega.Equal("ssh " + remoteUser + "@" + sshHost))

		projectName := workspace.ID
		eventsOutput, err := f.ExecCommandOutput(ctx, []string{
			cmdWorkspace, eventsCommand, resultFormatFlag, output.ModeJSON, projectName,
		})
		framework.ExpectNoError(err)
		var eventEnvelope struct {
			SchemaVersion int                      `json:"schemaVersion"`
			Events        []workspacejournal.Event `json:"events"`
		}
		framework.ExpectNoError(json.Unmarshal([]byte(eventsOutput), &eventEnvelope))
		gomega.Expect(eventEnvelope.SchemaVersion).To(gomega.Equal(workspacejournal.SchemaVersion))
		gomega.Expect(eventEnvelope.Events).NotTo(gomega.BeEmpty())
		gomega.Expect(eventEnvelope.Events[len(eventEnvelope.Events)-1]).To(gomega.SatisfyAll(
			gomega.HaveField("WorkspaceID", projectName),
			gomega.HaveField("Pipeline", status.PipelineWorkspaceUp),
			gomega.HaveField("State", status.StateSucceeded),
		))

		workspaceStatus, err := f.DevsyStatus(ctx, projectName)
		framework.ExpectNoError(err)
		gomega.Expect(workspaceStatus.LastOperation).NotTo(gomega.BeNil())
		gomega.Expect(workspaceStatus.LastOperation.Pipeline).
			To(gomega.Equal(string(status.PipelineWorkspaceUp)))
		gomega.Expect(workspaceStatus.LastOperation.State).
			To(gomega.Equal(string(status.StateSucceeded)))
		ids, err := dockerHelper.FindContainer(ctx, []string{
			fmt.Sprintf("%s=%s", pkgconfig.DevcontainerIDLabel, workspace.UID),
		})
		framework.ExpectNoError(err)
		gomega.Expect(ids).To(gomega.HaveLen(1), "1 compose container to be created")

		devcontainerPath := path.Join("/workspaces", projectName, ".devcontainer.json")

		containerEnvPath, _, err := f.ExecCommandCapture(
			ctx,
			[]string{cmdWorkspace, cmdSSH, flagCommand, "cat " + devcontainerPath, projectName},
		)
		framework.ExpectNoError(err)
		expectedImageName := language.MapConfig[language.Go].Image

		gomega.Expect(containerEnvPath).
			To(gomega.Equal(fmt.Sprintf("{\"image\":%q}", expectedImageName)))

		err = f.DevsyWorkspaceDelete(ctx, tempDir)
		framework.ExpectNoError(err)
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("recreate a local workspace", func(ctx context.Context) {
		const providerName = "test-docker"

		f := framework.NewDefaultFramework(initialDir + "/bin")
		tempDir, err := framework.CopyToTempDir("tests/up/testdata/no-devcontainer")
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

		// provider add, use and delete afterwards
		err = f.DevsyProviderAdd(ctx, dockerValue, "--name", providerName)
		framework.ExpectNoError(err)
		err = f.DevsyProviderUse(ctx, providerName)
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
			err := f.DevsyProviderDelete(cleanupCtx, providerName)
			framework.ExpectNoError(err)
		})

		err = f.DevsyUp(ctx, tempDir)
		framework.ExpectNoError(err)

		// recreate
		err = f.DevsyUpRecreate(ctx, tempDir)
		framework.ExpectNoError(err)

		err = f.DevsyWorkspaceDelete(ctx, tempDir)
		framework.ExpectNoError(err)
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("recreate a remote workspace", func(ctx context.Context) {
		const providerName = "test-docker"

		f := framework.NewDefaultFramework(initialDir + "/bin")

		// provider add, use and delete afterwards
		err := f.DevsyProviderAdd(ctx, dockerValue, "--name", providerName)
		framework.ExpectNoError(err)
		err = f.DevsyProviderUse(ctx, providerName)
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
			err := f.DevsyProviderDelete(cleanupCtx, providerName)
			framework.ExpectNoError(err)
		})

		id := "subpath--devsy-jupyter-notebook-hello-world"
		err = f.DevsyUp(
			ctx,
			"https://github.com/devsy-org/examples@subpath:/devsy/jupyter-notebook-hello-world",
		)
		framework.ExpectNoError(err)

		_, err = f.DevsySSH(ctx, id, "pwd")
		framework.ExpectNoError(err)

		// recreate
		err = f.DevsyUpRecreate(
			ctx,
			"https://github.com/devsy-org/examples@subpath:/devsy/jupyter-notebook-hello-world",
		)
		framework.ExpectNoError(err)

		_, err = f.DevsySSH(ctx, id, "pwd")
		framework.ExpectNoError(err)

		err = f.DevsyWorkspaceDelete(ctx, id)
		framework.ExpectNoError(err)
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("reset a remote workspace", func(ctx context.Context) {
		const providerName = "test-docker"

		f := framework.NewDefaultFramework(initialDir + "/bin")

		// provider add, use and delete afterwards
		err := f.DevsyProviderAdd(ctx, dockerValue, "--name", providerName)
		framework.ExpectNoError(err)
		err = f.DevsyProviderUse(ctx, providerName)
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
			err := f.DevsyProviderDelete(cleanupCtx, providerName)
			framework.ExpectNoError(err)
		})

		id := "subpath--devsy-jupyter-notebook-hello-world"
		err = f.DevsyUp(
			ctx,
			"https://github.com/loft-sh/examples@subpath:/devsy/jupyter-notebook-hello-world",
		)
		framework.ExpectNoError(err)

		// create files in root and in workspace, after create we expect data to still be there
		_, err = f.DevsySSH(ctx, id, fmt.Sprintf("sudo touch /workspaces/%s/DATA", id))
		framework.ExpectNoError(err)
		_, err = f.DevsySSH(ctx, id, "sudo touch /ROOTFS")
		framework.ExpectNoError(err)

		// reset
		err = f.DevsyUpReset(
			ctx,
			"https://github.com/loft-sh/examples@subpath:/devsy/jupyter-notebook-hello-world",
		)
		framework.ExpectNoError(err)

		// this should fail! because --reset should trigger a new git clone
		_, err = f.DevsySSH(ctx, id, fmt.Sprintf("ls /workspaces/%s/DATA", id))
		framework.ExpectError(err)
		// this should fail! because --reset should trigger a new build, so a new rootfs
		_, err = f.DevsySSH(ctx, id, "ls /ROOTFS")
		framework.ExpectError(err)

		err = f.DevsyWorkspaceDelete(ctx, id)
		framework.ExpectNoError(err)
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))
})
