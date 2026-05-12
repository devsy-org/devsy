package delivery

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/agent/delivery"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("agent delivery", ginkgo.Label("delivery"), func() {
	ginkgo.Context("LocalDockerDelivery", func() {
		ginkgo.It("should create volume, populate binary, and mount into RunOptions",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				d := &delivery.LocalDockerDelivery{DockerCommand: "docker"}
				workspaceID := "e2e-delivery-local"

				runOpts := &driver.RunOptions{
					Mounts: []*config.Mount{},
					Env:    map[string]string{},
				}

				binaryPath := findTestBinary()

				err := d.DeliverPreStart(ctx, delivery.PreStartOptions{
					WorkspaceID: workspaceID,
					RunOptions:  runOpts,
					BinaryPath:  binaryPath,
					Arch:        "amd64",
				})
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func() {
					_ = d.Cleanup(context.Background(), workspaceID)
				})

				gomega.Expect(runOpts.Mounts).To(gomega.HaveLen(1))
				expectedVolume := "devsy-agent-" + workspaceID
				gomega.Expect(runOpts.Mounts[0].Source).To(gomega.Equal(expectedVolume))
				gomega.Expect(runOpts.Mounts[0].Type).To(gomega.Equal("volume"))

				// Verify binary in volume via docker run
				out, err := exec.CommandContext(ctx, "docker", "run", "--rm",
					"-v", "devsy-agent-"+workspaceID+":/opt/devsy",
					"busybox:latest", "test", "-x", "/opt/devsy/devsy",
				).CombinedOutput()
				framework.ExpectNoError(
					err, "binary should be executable in volume: %s", string(out),
				)
			})
	})

	ginkgo.Context("RemoteDockerDelivery", func() {
		ginkgo.It("should copy binary into running container via docker cp",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				containerName := "e2e-delivery-remote"

				out, err := exec.CommandContext(ctx, "docker", "run", "-d",
					"--name", containerName,
					"busybox:latest", "sleep", "120",
				).CombinedOutput()
				framework.ExpectNoError(err, "failed to start test container: %s", string(out))
				ginkgo.DeferCleanup(func() {
					_ = exec.Command("docker", "rm", "-f", containerName).Run()
				})

				d := &delivery.RemoteDockerDelivery{
					DockerCommand: "docker",
					ContainerID:   containerName,
				}

				binaryPath := findTestBinary()

				err = d.DeliverPostStart(ctx, delivery.PostStartOptions{
					WorkspaceID: "e2e-workspace",
					BinaryPath:  binaryPath,
					Arch:        "amd64",
				})
				framework.ExpectNoError(err)

				// Verify binary exists and is executable
				out, err = exec.CommandContext(ctx, "docker", "exec", containerName,
					"test", "-x", "/usr/local/bin/devsy",
				).CombinedOutput()
				framework.ExpectNoError(err, "binary should be executable: %s", string(out))
			})
	})
})

func findTestBinary() string {
	candidates := []string{"/bin/sh", "/bin/busybox"}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}

	binDir, _ := os.Getwd()
	devsy := filepath.Join(binDir, "bin", "devsy-linux-amd64")
	if _, err := os.Stat(devsy); err == nil {
		return devsy
	}

	return "/bin/sh"
}
