package setup

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	setUpCommand   = "set-up"
	containerFlag  = "--container"
	configFlag     = "--config"
	alpineImage    = "alpine"
	dockerBin      = "docker"
	imageKey       = "image"
	devcontainerFn = "devcontainer.json"
)

var _ = ginkgo.Describe("devsy set-up command", ginkgo.Label("setup"), ginkgo.Ordered, func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.It("should execute postCreateCommand in a running container",
		func(ctx context.Context) {
			containerID := startAlpineContainer(ctx)
			ginkgo.DeferCleanup(removeContainer, containerID)

			tmpDir, err := framework.CreateTempDir()
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tmpDir)

			devcontainerJSON := map[string]any{
				imageKey:            alpineImage,
				"postCreateCommand": "touch /tmp/setup-test-marker",
			}
			writeDevcontainerJSON(tmpDir, devcontainerJSON)

			f := framework.NewDefaultFramework(initialDir + "/bin")
			_, _, err = f.ExecCommandCapture(ctx, []string{
				setUpCommand,
				containerFlag, containerID,
				configFlag, filepath.Join(tmpDir, ".devcontainer", devcontainerFn),
			})
			framework.ExpectNoError(err)

			out := dockerExecInContainer(ctx, containerID, "cat", "/tmp/setup-test-marker")
			gomega.Expect(out).To(gomega.BeEmpty())
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should execute postStartCommand in a running container",
		func(ctx context.Context) {
			containerID := startAlpineContainer(ctx)
			ginkgo.DeferCleanup(removeContainer, containerID)

			tmpDir, err := framework.CreateTempDir()
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tmpDir)

			devcontainerJSON := map[string]any{
				imageKey:           alpineImage,
				"postStartCommand": "touch /tmp/poststart-marker",
			}
			writeDevcontainerJSON(tmpDir, devcontainerJSON)

			f := framework.NewDefaultFramework(initialDir + "/bin")
			_, _, err = f.ExecCommandCapture(ctx, []string{
				setUpCommand,
				containerFlag, containerID,
				configFlag, filepath.Join(tmpDir, ".devcontainer", devcontainerFn),
			})
			framework.ExpectNoError(err)

			out := dockerExecInContainer(ctx, containerID, "cat", "/tmp/poststart-marker")
			gomega.Expect(out).To(gomega.BeEmpty())
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should pass containerEnv to lifecycle commands",
		func(ctx context.Context) {
			containerID := startAlpineContainer(ctx)
			ginkgo.DeferCleanup(removeContainer, containerID)

			tmpDir, err := framework.CreateTempDir()
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tmpDir)

			devcontainerJSON := map[string]any{
				imageKey:            alpineImage,
				"containerEnv":      map[string]string{"MY_VAR": "hello_world"},
				"postCreateCommand": "sh -c 'echo -n $MY_VAR > /tmp/env-marker'",
			}
			writeDevcontainerJSON(tmpDir, devcontainerJSON)

			f := framework.NewDefaultFramework(initialDir + "/bin")
			_, _, err = f.ExecCommandCapture(ctx, []string{
				setUpCommand,
				containerFlag, containerID,
				configFlag, filepath.Join(tmpDir, ".devcontainer", devcontainerFn),
			})
			framework.ExpectNoError(err)

			out := dockerExecInContainer(ctx, containerID, "cat", "/tmp/env-marker")
			gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("hello_world"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))
})

func startAlpineContainer(ctx context.Context) string {
	cmd := exec.CommandContext(
		ctx, dockerBin, "run", "-d", alpineImage, "sleep", "3600",
	)
	out, err := cmd.Output()
	framework.ExpectNoError(err)
	return strings.TrimSpace(string(out))
}

func removeContainer(containerID string) {
	// #nosec G204 -- test helper with controlled input
	cmd := exec.Command(dockerBin, "rm", "-f", containerID)
	_ = cmd.Run()
}

func dockerExecInContainer(
	ctx context.Context,
	containerID string,
	args ...string,
) string {
	execArgs := append([]string{"exec", containerID}, args...)
	// #nosec G204 -- test helper with controlled input
	cmd := exec.CommandContext(ctx, dockerBin, execArgs...)
	out, err := cmd.CombinedOutput()
	framework.ExpectNoError(err)
	return string(out)
}

func writeDevcontainerJSON(dir string, cfg map[string]any) {
	devcontainerDir := filepath.Join(dir, ".devcontainer")
	err := os.MkdirAll(devcontainerDir, 0o700)
	framework.ExpectNoError(err)

	data, err := json.Marshal(cfg)
	framework.ExpectNoError(err)

	err = os.WriteFile(
		filepath.Join(devcontainerDir, devcontainerFn), data, 0o600,
	)
	framework.ExpectNoError(err)
}
