package configapply

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
	configGroup    = "config"
	applyCommand   = "apply"
	containerFlag  = "--container"
	configFlag     = "--config"
	alpineImage    = "alpine"
	dockerBin      = "docker"
	imageKey       = "image"
	devcontainerFn = "devcontainer.json"

	postCreateCommandKey = "postCreateCommand"
)

var _ = ginkgo.Describe(
	"devsy config apply command",
	ginkgo.Label("config-apply"),
	ginkgo.Ordered,
	func() {
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
					imageKey:             alpineImage,
					postCreateCommandKey: "touch /tmp/setup-test-marker",
				}
				writeDevcontainerJSON(tmpDir, devcontainerJSON)

				f := framework.NewDefaultFramework(initialDir + "/bin")
				_, _, err = f.ExecCommandCapture(ctx, []string{
					configGroup, applyCommand,
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
					configGroup, applyCommand,
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
					imageKey:             alpineImage,
					"containerEnv":       map[string]string{"MY_VAR": "hello_world"},
					postCreateCommandKey: "sh -c 'echo -n $MY_VAR > /tmp/env-marker'",
				}
				writeDevcontainerJSON(tmpDir, devcontainerJSON)

				f := framework.NewDefaultFramework(initialDir + "/bin")
				_, _, err = f.ExecCommandCapture(ctx, []string{
					configGroup, applyCommand,
					containerFlag, containerID,
					configFlag, filepath.Join(tmpDir, ".devcontainer", devcontainerFn),
				})
				framework.ExpectNoError(err)

				out := dockerExecInContainer(ctx, containerID, "cat", "/tmp/env-marker")
				gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("hello_world"))
			}, ginkgo.SpecTimeout(framework.TimeoutShort()))

		ginkgo.It("should execute onCreateCommand in a running container",
			func(ctx context.Context) {
				containerID := startAlpineContainer(ctx)
				ginkgo.DeferCleanup(removeContainer, containerID)

				tmpDir, err := framework.CreateTempDir()
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tmpDir)

				devcontainerJSON := map[string]any{
					imageKey:          alpineImage,
					"onCreateCommand": "touch /tmp/oncreate-marker",
				}
				writeDevcontainerJSON(tmpDir, devcontainerJSON)

				f := framework.NewDefaultFramework(initialDir + "/bin")
				_, _, err = f.ExecCommandCapture(ctx, []string{
					configGroup, applyCommand,
					containerFlag, containerID,
					configFlag, filepath.Join(tmpDir, ".devcontainer", devcontainerFn),
				})
				framework.ExpectNoError(err)

				out := dockerExecInContainer(ctx, containerID, "cat", "/tmp/oncreate-marker")
				gomega.Expect(out).To(gomega.BeEmpty())
			}, ginkgo.SpecTimeout(framework.TimeoutShort()))

		ginkgo.It("should execute updateContentCommand in a running container",
			func(ctx context.Context) {
				containerID := startAlpineContainer(ctx)
				ginkgo.DeferCleanup(removeContainer, containerID)

				tmpDir, err := framework.CreateTempDir()
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tmpDir)

				devcontainerJSON := map[string]any{
					imageKey:               alpineImage,
					"updateContentCommand": "touch /tmp/updatecontent-marker",
				}
				writeDevcontainerJSON(tmpDir, devcontainerJSON)

				f := framework.NewDefaultFramework(initialDir + "/bin")
				_, _, err = f.ExecCommandCapture(ctx, []string{
					configGroup, applyCommand,
					containerFlag, containerID,
					configFlag, filepath.Join(tmpDir, ".devcontainer", devcontainerFn),
				})
				framework.ExpectNoError(err)

				out := dockerExecInContainer(ctx, containerID, "cat", "/tmp/updatecontent-marker")
				gomega.Expect(out).To(gomega.BeEmpty())
			}, ginkgo.SpecTimeout(framework.TimeoutShort()))

		ginkgo.It("should execute postAttachCommand in a running container",
			func(ctx context.Context) {
				containerID := startAlpineContainer(ctx)
				ginkgo.DeferCleanup(removeContainer, containerID)

				tmpDir, err := framework.CreateTempDir()
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tmpDir)

				devcontainerJSON := map[string]any{
					imageKey:            alpineImage,
					"postAttachCommand": "touch /tmp/postattach-marker",
				}
				writeDevcontainerJSON(tmpDir, devcontainerJSON)

				f := framework.NewDefaultFramework(initialDir + "/bin")
				_, _, err = f.ExecCommandCapture(ctx, []string{
					configGroup, applyCommand,
					containerFlag, containerID,
					configFlag, filepath.Join(tmpDir, ".devcontainer", devcontainerFn),
				})
				framework.ExpectNoError(err)

				out := dockerExecInContainer(ctx, containerID, "cat", "/tmp/postattach-marker")
				gomega.Expect(out).To(gomega.BeEmpty())
			}, ginkgo.SpecTimeout(framework.TimeoutShort()))

		ginkgo.It("should pass remoteEnv to lifecycle commands",
			func(ctx context.Context) {
				containerID := startAlpineContainer(ctx)
				ginkgo.DeferCleanup(removeContainer, containerID)

				tmpDir, err := framework.CreateTempDir()
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tmpDir)

				remoteEnvValue := "remote_env_works"
				devcontainerJSON := map[string]any{
					imageKey:             alpineImage,
					"remoteEnv":          map[string]string{"REMOTE_VAR": remoteEnvValue},
					postCreateCommandKey: "sh -c 'echo -n $REMOTE_VAR > /tmp/remoteenv-marker'",
				}
				writeDevcontainerJSON(tmpDir, devcontainerJSON)

				f := framework.NewDefaultFramework(initialDir + "/bin")
				_, _, err = f.ExecCommandCapture(ctx, []string{
					configGroup, applyCommand,
					containerFlag, containerID,
					configFlag, filepath.Join(tmpDir, ".devcontainer", devcontainerFn),
				})
				framework.ExpectNoError(err)

				out := dockerExecInContainer(ctx, containerID, "cat", "/tmp/remoteenv-marker")
				gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal(remoteEnvValue))
			}, ginkgo.SpecTimeout(framework.TimeoutShort()))

		ginkgo.It("should execute all lifecycle hooks in spec order",
			func(ctx context.Context) {
				containerID := startAlpineContainer(ctx)
				ginkgo.DeferCleanup(removeContainer, containerID)

				tmpDir, err := framework.CreateTempDir()
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tmpDir)

				devcontainerJSON := map[string]any{
					imageKey:               alpineImage,
					"onCreateCommand":      "sh -c 'echo -n 1 >> /tmp/order-marker'",
					"updateContentCommand": "sh -c 'echo -n 2 >> /tmp/order-marker'",
					postCreateCommandKey:   "sh -c 'echo -n 3 >> /tmp/order-marker'",
					"postStartCommand":     "sh -c 'echo -n 4 >> /tmp/order-marker'",
					"postAttachCommand":    "sh -c 'echo -n 5 >> /tmp/order-marker'",
				}
				writeDevcontainerJSON(tmpDir, devcontainerJSON)

				f := framework.NewDefaultFramework(initialDir + "/bin")
				_, _, err = f.ExecCommandCapture(ctx, []string{
					configGroup, applyCommand,
					containerFlag, containerID,
					configFlag, filepath.Join(tmpDir, ".devcontainer", devcontainerFn),
				})
				framework.ExpectNoError(err)

				out := dockerExecInContainer(ctx, containerID, "cat", "/tmp/order-marker")
				gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("12345"))
			}, ginkgo.SpecTimeout(framework.TimeoutShort()))

		ginkgo.It("should install a feature into the container",
			func(ctx context.Context) {
				containerID := startAlpineContainer(ctx)
				ginkgo.DeferCleanup(removeContainer, containerID)

				tmpDir, err := framework.CreateTempDir()
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tmpDir)

				localFeatureDir := filepath.Join(tmpDir, ".devcontainer", "my-feature")
				// #nosec G301 -- test helper creating feature directory structure
				err = os.MkdirAll(localFeatureDir, 0o750)
				framework.ExpectNoError(err)

				featureJSON := map[string]any{
					"id":      "my-feature",
					"version": "1.0.0",
					"name":    "My Test Feature",
				}
				featureData, err := json.Marshal(featureJSON)
				framework.ExpectNoError(err)
				err = os.WriteFile(
					filepath.Join(localFeatureDir, "devcontainer-feature.json"),
					featureData, 0o600,
				)
				framework.ExpectNoError(err)

				installScript := "#!/bin/sh\ntouch /tmp/feature-installed-marker\n"
				// #nosec G306 -- install script must be executable for feature installation
				err = os.WriteFile(
					filepath.Join(localFeatureDir, "install.sh"),
					[]byte(installScript), 0o600,
				)
				framework.ExpectNoError(err)

				devcontainerJSON := map[string]any{
					imageKey: alpineImage,
					"features": map[string]any{
						"./my-feature": map[string]any{},
					},
				}
				writeDevcontainerJSON(tmpDir, devcontainerJSON)

				f := framework.NewDefaultFramework(initialDir + "/bin")
				_, _, err = f.ExecCommandCapture(ctx, []string{
					configGroup, applyCommand,
					containerFlag, containerID,
					configFlag, filepath.Join(tmpDir, ".devcontainer", devcontainerFn),
				})
				framework.ExpectNoError(err)

				out := dockerExecInContainer(
					ctx,
					containerID,
					"cat",
					"/tmp/feature-installed-marker",
				)
				gomega.Expect(out).To(gomega.BeEmpty())
			}, ginkgo.SpecTimeout(framework.TimeoutShort()))
	},
)

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
