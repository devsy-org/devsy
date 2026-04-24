package up

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	docker "github.com/devsy-org/devsy/pkg/docker"
	"github.com/docker/docker/api/types/container"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"testing up command for docker customizations",
	ginkgo.Label("up-provider-docker"),
	func() {
		var dtc *dockerTestContext

		ginkgo.BeforeEach(func(ctx context.Context) {
			var err error
			dtc = &dockerTestContext{}
			dtc.initialDir, err = os.Getwd()
			framework.ExpectNoError(err)

			dtc.dockerHelper = &docker.DockerHelper{DockerCommand: "docker"}
			dtc.f, err = setupDockerProvider(filepath.Join(dtc.initialDir, "bin"), "docker")
			framework.ExpectNoError(err)
		})

		ginkgo.It("existing image", func(ctx context.Context) {
			_, err := dtc.setupAndUp(ctx, "tests/up/testdata/docker")
			framework.ExpectNoError(err)
		}, ginkgo.SpecTimeout(framework.GetTimeout()))

		ginkgo.It("existing running container", func(ctx context.Context) {
			tempDir, err := framework.CopyToTempDir("tests/up/testdata/no-devcontainer")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, dtc.initialDir, tempDir)
			ginkgo.DeferCleanup(dtc.f.DevsyWorkspaceDelete, tempDir)

			err = dtc.dockerHelper.Run(
				ctx,
				[]string{
					"run",
					"-d",
					"--label",
					"devsy-e2e-test-container=true",
					"-w",
					"/workspaces/e2e",
					"alpine",
					"sleep",
					"infinity",
				},
				nil,
				nil,
				nil,
			)
			framework.ExpectNoError(err)

			var ids []string
			gomega.Eventually(func() bool {
				ids, err = dtc.dockerHelper.FindContainer(
					ctx,
					[]string{"devsy-e2e-test-container=true"},
				)
				if err != nil || len(ids) != 1 {
					return false
				}
				var containerDetails []container.InspectResponse
				err = dtc.dockerHelper.Inspect(ctx, ids, "container", &containerDetails)
				return err == nil && containerDetails[0].State.Running
			}).WithTimeout(30 * time.Second).WithPolling(1 * time.Second).Should(gomega.BeTrue())

			ginkgo.DeferCleanup(dtc.dockerHelper.Remove, ids[0])
			ginkgo.DeferCleanup(dtc.dockerHelper.Stop, ids[0])

			var containerDetails []container.InspectResponse
			err = dtc.dockerHelper.Inspect(ctx, ids, "container", &containerDetails)
			framework.ExpectNoError(err)
			gomega.Expect(containerDetails[0].Config.WorkingDir).To(gomega.Equal("/workspaces/e2e"))

			err = dtc.f.DevsyUp(
				ctx,
				tempDir,
				"--source",
				fmt.Sprintf("container:%s", containerDetails[0].ID),
			)
			framework.ExpectNoError(err)
		}, ginkgo.SpecTimeout(framework.GetTimeout()))

		ginkgo.It("variables substitution", func(ctx context.Context) {
			tempDir, err := dtc.setupAndUp(ctx, "tests/up/testdata/docker-variables",
				"--init-env", "CUSTOM_VAR=custom_value",
				"--init-env", "CUSTOM_IMAGE=alpine:latest")
			framework.ExpectNoError(err)

			workspace, err := dtc.f.FindWorkspace(ctx, tempDir)
			framework.ExpectNoError(err)

			ids, err := dtc.findWorkspaceContainer(ctx, workspace)
			framework.ExpectNoError(err)
			gomega.Expect(ids).To(gomega.HaveLen(1))

			devContainerID, err := dtc.execSSHCapture(
				ctx,
				workspace.ID,
				"cat $HOME/dev-container-id.out",
			)
			framework.ExpectNoError(err)
			gomega.Expect(devContainerID).NotTo(gomega.BeEmpty())

			containerEnvPath, err := dtc.execSSHCapture(
				ctx,
				workspace.ID,
				"cat $HOME/container-env-path.out",
			)
			framework.ExpectNoError(err)
			gomega.Expect(containerEnvPath).To(gomega.ContainSubstring("/usr/local/bin"))

			localEnvHome, err := dtc.execSSHCapture(
				ctx,
				workspace.ID,
				"cat $HOME/local-env-home.out",
			)
			framework.ExpectNoError(err)
			gomega.Expect(localEnvHome).To(gomega.Equal(os.Getenv("HOME")))

			localWorkspaceFolder, err := dtc.execSSHCapture(
				ctx,
				workspace.ID,
				"cat $HOME/local-workspace-folder.out",
			)
			framework.ExpectNoError(err)
			gomega.Expect(framework.CleanString(localWorkspaceFolder)).
				To(gomega.Equal(framework.CleanString(tempDir)))

			localWorkspaceFolderBasename, err := dtc.execSSHCapture(
				ctx,
				workspace.ID,
				"cat $HOME/local-workspace-folder-basename.out",
			)
			framework.ExpectNoError(err)
			gomega.Expect(localWorkspaceFolderBasename).To(gomega.Equal(filepath.Base(tempDir)))

			containerWorkspaceFolder, err := dtc.execSSHCapture(
				ctx,
				workspace.ID,
				"cat $HOME/container-workspace-folder.out",
			)
			framework.ExpectNoError(err)
			gomega.Expect(framework.CleanString(containerWorkspaceFolder)).
				To(gomega.Equal(framework.CleanString(path.Join("/workspaces", filepath.Base(tempDir)))))

			containerWorkspaceFolderBasename, err := dtc.execSSHCapture(
				ctx,
				workspace.ID,
				"cat $HOME/container-workspace-folder-basename.out",
			)
			framework.ExpectNoError(err)
			gomega.Expect(containerWorkspaceFolderBasename).To(gomega.Equal(filepath.Base(tempDir)))

			customVar, err := dtc.execSSHCapture(ctx, workspace.ID, "cat $HOME/custom-var.out")
			framework.ExpectNoError(err)
			gomega.Expect(customVar).To(gomega.Equal("custom_value"))

			customImage, err := dtc.execSSHCapture(ctx, workspace.ID, "cat $HOME/custom-image.out")
			framework.ExpectNoError(err)
			gomega.Expect(customImage).To(gomega.Equal("alpine:latest"))
		}, ginkgo.SpecTimeout(framework.GetTimeout()))

		ginkgo.It("variable substitution with defaults", func(ctx context.Context) {
			tempDir, err := dtc.setupAndUp(
				ctx, "tests/up/testdata/docker-variables-defaults",
			)
			framework.ExpectNoError(err)

			workspace, err := dtc.f.FindWorkspace(ctx, tempDir)
			framework.ExpectNoError(err)

			ids, err := dtc.findWorkspaceContainer(ctx, workspace)
			framework.ExpectNoError(err)
			gomega.Expect(ids).To(gomega.HaveLen(1))

			// Unset var uses simple default
			withDefault, err := dtc.execSSHCapture(
				ctx, workspace.ID,
				"cat $HOME/with-default.out",
			)
			framework.ExpectNoError(err)
			gomega.Expect(withDefault).To(
				gomega.Equal("my_default_value"),
			)

			// Unset var uses default containing colons
			colonDefault, err := dtc.execSSHCapture(
				ctx, workspace.ID,
				"cat $HOME/colon-default.out",
			)
			framework.ExpectNoError(err)
			gomega.Expect(colonDefault).To(
				gomega.Equal("http://proxy:8080"),
			)

			// Set var ignores default
			setVar, err := dtc.execSSHCapture(
				ctx, workspace.ID,
				"cat $HOME/set-var.out",
			)
			framework.ExpectNoError(err)
			gomega.Expect(setVar).To(
				gomega.Equal(os.Getenv("HOME")),
			)
		}, ginkgo.SpecTimeout(framework.GetTimeout()))

		ginkgo.It("unknown variables resolve to empty string", func(ctx context.Context) {
			tempDir, err := dtc.setupAndUp(
				ctx, "tests/up/testdata/docker-variables-unknown",
			)
			framework.ExpectNoError(err)

			workspace, err := dtc.f.FindWorkspace(ctx, tempDir)
			framework.ExpectNoError(err)

			ids, err := dtc.findWorkspaceContainer(ctx, workspace)
			framework.ExpectNoError(err)
			gomega.Expect(ids).To(gomega.HaveLen(1))

			output, err := dtc.execSSHCapture(
				ctx, workspace.ID,
				"cat $HOME/unknown-var.out",
			)
			framework.ExpectNoError(err)
			gomega.Expect(output).To(
				gomega.Equal("UNKNOWN=KNOWN=hello"),
			)
		}, ginkgo.SpecTimeout(framework.GetTimeout()))

		ginkgo.It("mounts", func(ctx context.Context) {
			tempDir, err := dtc.setupAndUp(ctx, "tests/up/testdata/docker-mounts", "--debug")
			framework.ExpectNoError(err)

			workspace, err := dtc.f.FindWorkspace(ctx, tempDir)
			framework.ExpectNoError(err)

			ids, err := dtc.findWorkspaceContainer(ctx, workspace)
			framework.ExpectNoError(err)
			gomega.Expect(ids).To(gomega.HaveLen(1))

			foo, err := dtc.execSSHCapture(ctx, workspace.ID, "cat $HOME/mnt1/foo.txt")
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(foo)).To(gomega.Equal("BAR"))

			bar, err := dtc.execSSHCapture(ctx, workspace.ID, "cat $HOME/mnt2/bar.txt")
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(bar)).To(gomega.Equal("FOO"))
		}, ginkgo.SpecTimeout(framework.GetTimeout()))

		ginkgo.It("custom image", func(ctx context.Context) {
			if runtime.GOOS == "windows" {
				ginkgo.Skip("skipping on windows")
			}

			tempDir, err := dtc.setupAndUp(
				ctx,
				"tests/up/testdata/docker",
				"--devcontainer-image",
				"alpine",
			)
			framework.ExpectNoError(err)

			out, err := dtc.execSSH(ctx, tempDir, "grep ^ID= /etc/os-release")
			framework.ExpectNoError(err)
			framework.ExpectEqual(out, "ID=alpine\n")
		}, ginkgo.SpecTimeout(framework.GetTimeout()))

		ginkgo.It("custom image skip build", func(ctx context.Context) {
			tempDir, err := dtc.setupAndUp(
				ctx,
				"tests/up/testdata/docker-with-multi-stage-build",
				"--devcontainer-image",
				"alpine",
			)
			framework.ExpectNoError(err)

			out, err := dtc.execSSH(ctx, tempDir, "grep ^ID= /etc/os-release")
			framework.ExpectNoError(err)
			framework.ExpectEqual(out, "ID=alpine\n")
		}, ginkgo.SpecTimeout(framework.GetTimeout()))

		ginkgo.It("extra devcontainer merge", func(ctx context.Context) {
			tempDir, err := setupWorkspace(
				"tests/up/testdata/docker-extra-devcontainer",
				dtc.initialDir,
				dtc.f,
			)
			framework.ExpectNoError(err)

			extraPath := path.Join(tempDir, "extra.json")
			err = dtc.f.DevsyUp(ctx, tempDir, "--extra-devcontainer-path", extraPath)
			framework.ExpectNoError(err)

			out, err := dtc.execSSH(ctx, tempDir, "bash -l -c 'echo -n $BASE_VAR'")
			framework.ExpectNoError(err)
			framework.ExpectEqual(out, "base_value")

			out, err = dtc.execSSH(ctx, tempDir, "bash -l -c 'echo -n $EXTRA_VAR'")
			framework.ExpectNoError(err)
			framework.ExpectEqual(out, "extra_value")

			err = dtc.f.DevsyWorkspaceDelete(ctx, tempDir)
			framework.ExpectNoError(err)
		}, ginkgo.SpecTimeout(framework.GetTimeout()))

		ginkgo.It("extra devcontainer override", func(ctx context.Context) {
			tempDir, err := setupWorkspace(
				"tests/up/testdata/docker-extra-override",
				dtc.initialDir,
				dtc.f,
			)
			framework.ExpectNoError(err)

			extraPath := path.Join(tempDir, "override.json")
			err = dtc.f.DevsyUp(ctx, tempDir, "--extra-devcontainer-path", extraPath)
			framework.ExpectNoError(err)

			out, err := dtc.execSSH(ctx, tempDir, "cat /tmp/test-var.out")
			framework.ExpectNoError(err)
			framework.ExpectEqual(strings.TrimSpace(out), "overridden_value")

			err = dtc.f.DevsyWorkspaceDelete(ctx, tempDir)
			framework.ExpectNoError(err)
		}, ginkgo.SpecTimeout(framework.GetTimeout()))

		ginkgo.It("postStartCommand runs after restart", func(ctx context.Context) {
			tempDir, err := setupWorkspace(
				"tests/up/testdata/docker-post-start-restart",
				dtc.initialDir,
				dtc.f,
			)
			framework.ExpectNoError(err)

			// First up: postStartCommand should run
			err = dtc.f.DevsyUp(ctx, tempDir)
			framework.ExpectNoError(err)

			out, err := dtc.execSSH(ctx, tempDir, "cat $HOME/post-start-count.log")
			framework.ExpectNoError(err)
			lines := strings.Count(strings.TrimSpace(out), "\n") + 1
			gomega.Expect(lines).To(gomega.Equal(1),
				"postStartCommand should have run once after initial up")

			// Stop the workspace
			err = dtc.f.DevsyWorkspaceStop(ctx, tempDir)
			framework.ExpectNoError(err)

			// Second up (restart): postStartCommand should run again
			err = dtc.f.DevsyUp(ctx, tempDir)
			framework.ExpectNoError(err)

			out, err = dtc.execSSH(ctx, tempDir, "cat $HOME/post-start-count.log")
			framework.ExpectNoError(err)
			lines = strings.Count(strings.TrimSpace(out), "\n") + 1
			gomega.Expect(lines).To(gomega.Equal(2),
				"postStartCommand should have run again after restart")
		}, ginkgo.SpecTimeout(framework.GetTimeout()))

		ginkgo.It("waitFor defers postCreateCommand to background", func(ctx context.Context) {
			tempDir, err := setupWorkspace(
				"tests/up/testdata/docker-waitfor",
				dtc.initialDir,
				dtc.f,
			)
			framework.ExpectNoError(err)

			err = dtc.f.DevsyUp(ctx, tempDir)
			framework.ExpectNoError(err)

			// onCreateCommand and updateContentCommand should have run (foreground).
			out, err := dtc.execSSH(ctx, tempDir, "cat $HOME/on-create.out")
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("onCreateDone"))

			out, err = dtc.execSSH(ctx, tempDir, "cat $HOME/update-content.out")
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("updateContentDone"))

			// postCreateCommand runs as a deferred hook in the background.
			// Wait for it to complete and verify the marker file + env substitution.
			gomega.Eventually(func() string {
				out, err := dtc.execSSH(ctx, tempDir, "cat $HOME/deferred.marker 2>/dev/null")
				if err != nil {
					return ""
				}
				return strings.TrimSpace(out)
			}).WithTimeout(30*time.Second).WithPolling(2*time.Second).Should(
				gomega.Equal("postCreateDone"),
				"deferred postCreateCommand should eventually complete in background",
			)

			// Verify the deferred hook received substituted env vars, not literals.
			envPath, err := dtc.execSSH(ctx, tempDir, "cat $HOME/deferred-env-path.out")
			framework.ExpectNoError(err)
			gomega.Expect(envPath).To(gomega.ContainSubstring("/usr/local/bin"),
				"deferred hook should receive resolved PATH, not ${containerEnv:PATH}")
			gomega.Expect(envPath).NotTo(gomega.ContainSubstring("${containerEnv:"),
				"deferred hook should not contain literal variable references")

			// postStartCommand also deferred — verify it ran.
			gomega.Eventually(func() string {
				out, err := dtc.execSSH(
					ctx,
					tempDir,
					"cat $HOME/post-start-deferred.out 2>/dev/null",
				)
				if err != nil {
					return ""
				}
				return strings.TrimSpace(out)
			}).WithTimeout(30*time.Second).WithPolling(2*time.Second).Should(
				gomega.Equal("postStartDone"),
				"deferred postStartCommand should eventually complete in background",
			)
		}, ginkgo.SpecTimeout(framework.GetTimeout()))

		ginkgo.It("IDE accessible before postAttachCommand completes", func(ctx context.Context) {
			tempDir, err := setupWorkspace(
				"tests/up/testdata/docker-post-attach-nonblocking",
				dtc.initialDir,
				dtc.f,
			)
			framework.ExpectNoError(err)

			// devsy up with --ide none returns when IDE would open,
			// which should now be BEFORE postAttachCommand finishes
			err = dtc.f.DevsyUp(ctx, tempDir)
			framework.ExpectNoError(err)

			// postStartCommand should have completed (runs before result is sent)
			out, err := dtc.execSSH(ctx, tempDir, "cat $HOME/post-start.out")
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("postStartDone"),
				"postStartCommand should have completed before devsy up returned")

			// postAttachCommand should NOT have completed yet (it sleeps 15s)
			_, err = dtc.execSSH(ctx, tempDir, "cat $HOME/post-attach.out")
			gomega.Expect(err).To(gomega.HaveOccurred(),
				"postAttachCommand should still be running when devsy up returns")

			// Wait for postAttachCommand to finish and verify it does complete
			gomega.Eventually(func() string {
				out, err := dtc.execSSH(ctx, tempDir, "cat $HOME/post-attach.out 2>/dev/null")
				if err != nil {
					return ""
				}
				return strings.TrimSpace(out)
			}).WithTimeout(30*time.Second).WithPolling(2*time.Second).Should(
				gomega.Equal("postAttachDone"),
				"postAttachCommand should eventually complete in the background",
			)
		}, ginkgo.SpecTimeout(framework.GetTimeout()))

		ginkgo.It("multi devcontainer selection", func(ctx context.Context) {
			tempDir, err := setupWorkspace(
				"tests/up/testdata/docker-multi-devcontainer",
				dtc.initialDir,
				dtc.f,
			)
			framework.ExpectNoError(err)

			err = dtc.f.DevsyUp(ctx, tempDir, "--devcontainer-id", "python")
			framework.ExpectNoError(err)

			out, err := dtc.execSSH(ctx, tempDir, "bash -l -c 'echo -n $DEVCONTAINER_TYPE'")
			framework.ExpectNoError(err)
			framework.ExpectEqual(out, "python")

			err = dtc.f.DevsyWorkspaceDelete(ctx, tempDir)
			framework.ExpectNoError(err)

			err = dtc.f.DevsyUp(ctx, tempDir, "--devcontainer-id", "go")
			framework.ExpectNoError(err)

			out, err = dtc.execSSH(ctx, tempDir, "bash -l -c 'echo -n $DEVCONTAINER_TYPE'")
			framework.ExpectNoError(err)
			framework.ExpectEqual(out, "go")

			err = dtc.f.DevsyWorkspaceDelete(ctx, tempDir)
			framework.ExpectNoError(err)
		}, ginkgo.SpecTimeout(framework.GetTimeout()))
	},
)
