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

		tempDir, err := framework.CopyToTempDir("tests/down/testdata/docker")
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

		err = f.DevsyUp(ctx, tempDir)
		framework.ExpectNoError(err)

		// Verify workspace is running
		status, err := f.DevsyStatus(ctx, tempDir)
		framework.ExpectNoError(err)
		gomega.Expect(strings.ToUpper(status.State)).To(gomega.Equal("RUNNING"))

		// Get workspace UID for container lookup
		workspace, err := f.FindWorkspace(ctx, tempDir)
		framework.ExpectNoError(err)

		containerIDs, err := dockerHelper.FindContainer(ctx, []string{
			fmt.Sprintf("%s=%s", config.DockerIDLabel, workspace.UID),
		})
		framework.ExpectNoError(err)
		gomega.Expect(containerIDs).NotTo(gomega.BeEmpty(), "container should exist before down")

		// Run devsy down
		err = f.DevsyDown(ctx, tempDir)
		framework.ExpectNoError(err)

		// Verify container is gone
		containerIDs, err = dockerHelper.FindContainer(ctx, []string{
			fmt.Sprintf("%s=%s", config.DockerIDLabel, workspace.UID),
		})
		framework.ExpectNoError(err)
		gomega.Expect(containerIDs).To(gomega.BeEmpty(), "container should be deleted after down")

		// Verify workspace no longer appears in list
		_, err = f.FindWorkspace(ctx, tempDir)
		gomega.Expect(err).To(gomega.HaveOccurred(), "workspace should not be in list after down")
	}, ginkgo.SpecTimeout(framework.TimeoutModerate()))

	ginkgo.It("stop only stops and does not delete workspace", func(ctx context.Context) {
		f, err := framework.SetupDockerProvider(initialDir+"/bin", "docker")
		framework.ExpectNoError(err)

		tempDir, err := framework.CopyToTempDir("tests/down/testdata/docker")
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
		ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

		err = f.DevsyUp(ctx, tempDir)
		framework.ExpectNoError(err)

		// Verify workspace is running
		status, err := f.DevsyStatus(ctx, tempDir)
		framework.ExpectNoError(err)
		gomega.Expect(strings.ToUpper(status.State)).To(gomega.Equal("RUNNING"))

		// Get workspace UID for container lookup
		workspace, err := f.FindWorkspace(ctx, tempDir)
		framework.ExpectNoError(err)

		// Run devsy stop
		err = f.DevsyStop(ctx, tempDir)
		framework.ExpectNoError(err)

		// Verify workspace still exists in list (just stopped, not deleted)
		_, err = f.FindWorkspace(ctx, tempDir)
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
