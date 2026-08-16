package down

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	docker "github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"testing workspace delete command",
	ginkgo.Label("down"),
	func() {
		var dockerHelper *docker.DockerHelper
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)

			dockerHelper = &docker.DockerHelper{DockerCommand: "docker"}
		})

		ginkgo.It("workspace delete stops and deletes workspace", func(ctx context.Context) {
			f, err := framework.SetupDockerProvider(initialDir+"/bin", "docker")
			framework.ExpectNoError(err)

			tempDir, err := framework.CopyToTempDir("tests/down/testdata/docker")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

			err = f.DevsyUp(ctx, tempDir)
			framework.ExpectNoError(err)

			status, err := f.DevsyStatus(ctx, tempDir)
			framework.ExpectNoError(err)
			gomega.Expect(strings.ToUpper(status.State)).To(gomega.Equal("RUNNING"))

			workspace, err := f.FindWorkspace(ctx, tempDir)
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

			containerIDs, err := dockerHelper.FindContainer(ctx, []string{
				fmt.Sprintf("%s=%s", pkgconfig.DevcontainerIDLabel, workspace.UID),
			})
			framework.ExpectNoError(err)
			gomega.Expect(containerIDs).
				NotTo(gomega.BeEmpty(), "container should exist before delete")

			err = f.DevsyWorkspaceDelete(ctx, tempDir)
			framework.ExpectNoError(err)

			containerIDs, err = dockerHelper.FindContainer(ctx, []string{
				fmt.Sprintf("%s=%s", pkgconfig.DevcontainerIDLabel, workspace.UID),
			})
			framework.ExpectNoError(err)
			gomega.Expect(containerIDs).
				To(gomega.BeEmpty(), "container should be deleted after delete")

			_, err = f.FindWorkspace(ctx, tempDir)
			gomega.Expect(err).
				To(gomega.HaveOccurred(), "workspace should not be in list after delete")
		}, ginkgo.SpecTimeout(framework.TimeoutModerate()))

		ginkgo.It(
			"workspace delete removes workspace with restrictive folder permissions",
			func(ctx context.Context) {
				f, err := framework.SetupDockerProvider(initialDir+"/bin", "docker")
				framework.ExpectNoError(err)

				name := "vscode-remote-try-python"
				ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, name)

				err = f.DevsyUp(ctx, "https://github.com/microsoft/vscode-remote-try-python.git")
				framework.ExpectNoError(err)

				workspace, err := f.FindWorkspace(ctx, name)
				framework.ExpectNoError(err)

				if workspace.Source.LocalFolder != "" {
					folder := workspace.Source.LocalFolder
					err = os.Chmod(folder, 0o500) //nolint:gosec
					framework.ExpectNoError(err)
				}

				err = f.DevsyWorkspaceDelete(ctx, name)
				framework.ExpectNoError(err)

				_, err = f.FindWorkspace(ctx, name)
				gomega.Expect(err).
					To(gomega.HaveOccurred(), "workspace should not be in list after delete")
			},
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
		)

		ginkgo.It("stop only stops and does not delete workspace", func(ctx context.Context) {
			f, err := framework.SetupDockerProvider(initialDir+"/bin", "docker")
			framework.ExpectNoError(err)

			tempDir, err := framework.CopyToTempDir("tests/down/testdata/docker")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
			ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

			err = f.DevsyUp(ctx, tempDir)
			framework.ExpectNoError(err)

			status, err := f.DevsyStatus(ctx, tempDir)
			framework.ExpectNoError(err)
			gomega.Expect(strings.ToUpper(status.State)).To(gomega.Equal("RUNNING"))

			workspace, err := f.FindWorkspace(ctx, tempDir)
			framework.ExpectNoError(err)

			err = f.DevsyStop(ctx, tempDir)
			framework.ExpectNoError(err)

			_, err = f.FindWorkspace(ctx, tempDir)
			framework.ExpectNoError(err)

			containerIDs, err := dockerHelper.FindContainer(ctx, []string{
				fmt.Sprintf("%s=%s", pkgconfig.DevcontainerIDLabel, workspace.UID),
			})
			framework.ExpectNoError(err)
			gomega.Expect(containerIDs).NotTo(
				gomega.BeEmpty(),
				"container should still exist after stop (only stopped, not deleted)",
			)
		}, ginkgo.SpecTimeout(framework.TimeoutModerate()))
	},
)
