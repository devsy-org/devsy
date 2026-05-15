package down

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	docker "github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("testing down command", ginkgo.Label("down"), func() {
	var dockerHelper *docker.DockerHelper
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)

		dockerHelper = &docker.DockerHelper{DockerCommand: "docker"}
	})

	ginkgo.It("down stops and deletes workspace", func(ctx context.Context) {
		f, err := framework.SetupDockerProvider(initialDir+"/bin", "docker")
		framework.ExpectNoError(err)

		name := "vscode-remote-try-python"

		err = f.DevsyUp(ctx, "https://github.com/microsoft/vscode-remote-try-python.git")
		framework.ExpectNoError(err)

		// Verify workspace is running
		status, err := f.DevsyStatus(ctx, name)
		framework.ExpectNoError(err)
		gomega.Expect(strings.ToUpper(status.State)).To(gomega.Equal("RUNNING"))

		// Get workspace UID for container lookup
		workspace, err := f.FindWorkspace(ctx, name)
		framework.ExpectNoError(err)

		containerIDs, err := dockerHelper.FindContainer(ctx, []string{
			fmt.Sprintf("%s=%s", config.DockerIDLabel, workspace.UID),
		})
		framework.ExpectNoError(err)
		gomega.Expect(containerIDs).NotTo(gomega.BeEmpty(), "container should exist before down")

		// Run devsy down
		err = f.DevsyDown(ctx, name)
		framework.ExpectNoError(err)

		// Verify container is gone
		containerIDs, err = dockerHelper.FindContainer(ctx, []string{
			fmt.Sprintf("%s=%s", config.DockerIDLabel, workspace.UID),
		})
		framework.ExpectNoError(err)
		gomega.Expect(containerIDs).To(gomega.BeEmpty(), "container should be deleted after down")

		// Verify workspace no longer appears in list
		_, err = f.FindWorkspace(ctx, name)
		gomega.Expect(err).To(gomega.HaveOccurred(), "workspace should not be in list after down")
	}, ginkgo.SpecTimeout(framework.TimeoutModerate()))

	ginkgo.It(
		"down deletes workspace with restrictive folder permissions",
		func(ctx context.Context) {
			f, err := framework.SetupDockerProvider(initialDir+"/bin", "docker")
			framework.ExpectNoError(err)

			name := "vscode-remote-try-python"
			ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, name)

			err = f.DevsyUp(ctx, "https://github.com/microsoft/vscode-remote-try-python.git")
			framework.ExpectNoError(err)

			// Get workspace to find local folder path
			workspace, err := f.FindWorkspace(ctx, name)
			framework.ExpectNoError(err)

			// Set restrictive permissions on the workspace source folder to simulate the bug
			// where container runtime leaves folders with 0500 permissions
			if workspace.Source.LocalFolder != "" {
				folder := workspace.Source.LocalFolder
				err = os.Chmod(folder, 0o500) //nolint:gosec
				framework.ExpectNoError(err)
			}

			// down should succeed even with restrictive permissions
			err = f.DevsyDown(ctx, name)
			framework.ExpectNoError(err)

			// Verify workspace no longer appears in list
			_, err = f.FindWorkspace(ctx, name)
			gomega.Expect(err).
				To(gomega.HaveOccurred(), "workspace should not be in list after down")
		},
		ginkgo.SpecTimeout(framework.TimeoutModerate()),
	)

	ginkgo.It("stop only stops and does not delete workspace", func(ctx context.Context) {
		f, err := framework.SetupDockerProvider(initialDir+"/bin", "docker")
		framework.ExpectNoError(err)

		name := "vscode-remote-try-python"
		ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, name)

		err = f.DevsyUp(ctx, "https://github.com/microsoft/vscode-remote-try-python.git")
		framework.ExpectNoError(err)

		// Verify workspace is running
		status, err := f.DevsyStatus(ctx, name)
		framework.ExpectNoError(err)
		gomega.Expect(strings.ToUpper(status.State)).To(gomega.Equal("RUNNING"))

		// Get workspace UID for container lookup
		workspace, err := f.FindWorkspace(ctx, name)
		framework.ExpectNoError(err)

		// Run devsy stop
		err = f.DevsyStop(ctx, name)
		framework.ExpectNoError(err)

		// Verify workspace still exists in list (just stopped, not deleted)
		_, err = f.FindWorkspace(ctx, name)
		framework.ExpectNoError(err)

		// Verify container still exists (stopped but not removed)
		containerIDs, err := dockerHelper.FindContainer(ctx, []string{
			fmt.Sprintf("%s=%s", config.DockerIDLabel, workspace.UID),
		})
		framework.ExpectNoError(err)
		gomega.Expect(containerIDs).NotTo(
			gomega.BeEmpty(),
			"container should still exist after stop (only stopped, not deleted)",
		)
	}, ginkgo.SpecTimeout(framework.TimeoutModerate()))
})
