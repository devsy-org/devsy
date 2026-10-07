//go:build linux || darwin || unix

package up

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/compose"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	docker "github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	recreateLifecycleDockerCommand = "docker"
	recreateLifecycleImageCommand  = "image"
)

var _ = ginkgo.Describe(
	"Compose recreation lifecycle metadata",
	ginkgo.Label("up-docker-compose"),
	func() {
		var tc *testContext

		ginkgo.BeforeEach(func() {
			var err error
			tc = &testContext{}
			tc.initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
			tc.dockerHelper = &docker.DockerHelper{DockerCommand: recreateLifecycleDockerCommand}
			tc.composeHelper, err = compose.NewComposeHelper(tc.dockerHelper)
			framework.ExpectNoError(err)
			tc.f, err = setupDockerProvider(filepath.Join(tc.initialDir, "bin"))
			framework.ExpectNoError(err)
		})

		for _, recreateArgs := range [][]string{
			{"--recreate"},
			{"--reset", "--recreate"},
		} {
			args := append([]string(nil), recreateArgs...)
			ginkgo.It(
				fmt.Sprintf("refreshes lifecycle metadata with %s", strings.Join(args, " ")),
				func(ctx context.Context) {
					fixturePath := filepath.Join(
						tc.initialDir,
						"tests/up-docker-compose/testdata/docker-compose-recreate-lifecycle",
					)
					imageTag := fmt.Sprintf(
						"devsy-e2e-recreate-lifecycle:%d",
						time.Now().UnixNano(),
					)
					var buildStdout, buildStderr bytes.Buffer
					buildErr := tc.dockerHelper.Run(
						ctx,
						[]string{"build", "--tag", imageTag, fixturePath},
						docker.Streams{
							Stdout: &buildStdout,
							Stderr: &buildStderr,
						},
					)
					framework.ExpectNoError(
						buildErr,
						"docker build output\nstdout: %s\nstderr: %s",
						buildStdout.String(),
						buildStderr.String(),
					)
					ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
						_ = tc.dockerHelper.Run(
							cleanupCtx,
							[]string{recreateLifecycleImageCommand, "rm", "--force", imageTag},
							docker.Streams{
								Stdout: io.Discard,
								Stderr: io.Discard,
							},
						)
					})

					tempDir, err := setupWorkspace(
						"tests/up-docker-compose/testdata/docker-compose-recreate-lifecycle",
						tc.initialDir,
						tc.f,
					)
					framework.ExpectNoError(err)
					ginkgo.DeferCleanup(tc.f.DevsyWorkspaceDelete, tempDir)
					composePath := filepath.Join(tempDir, "docker-compose.yaml")
					// #nosec G304 -- composePath is within the temporary workspace created for this test.
					composeConfig, err := os.ReadFile(composePath)
					framework.ExpectNoError(err)
					composeConfig = []byte(
						strings.Replace(string(composeConfig), "IMAGE_PLACEHOLDER", imageTag, 1),
					)
					framework.ExpectNoError(os.WriteFile(composePath, composeConfig, 0o600))
					framework.ExpectNoError(tc.f.DevsyUp(ctx, tempDir))
					workspace, err := tc.f.FindWorkspace(ctx, tempDir)
					framework.ExpectNoError(err)

					before, err := tc.getAppContainer(ctx, workspace)
					framework.ExpectNoError(err)
					initialMetadata := before.Config.Labels[pkgconfig.DevcontainerMetadataLabel]
					gomega.Expect(initialMetadata).To(gomega.ContainSubstring("USER_HOOK_V1"))
					gomega.Expect(initialMetadata).To(gomega.ContainSubstring("REMOVED_USER_HOOK"))
					gomega.Expect(initialMetadata).To(gomega.ContainSubstring("STABLE_USER_HOOK"))
					initialProject := before.Config.Labels[pkgconfig.ComposeProjectLabel]
					gomega.Expect(initialMetadata).
						To(gomega.ContainSubstring("IMAGE_LIFECYCLE_HOOK"))

					configPath := filepath.Join(tempDir, ".devcontainer.json")
					// #nosec G304 -- configPath is within the temporary workspace created for this test.
					config, err := os.ReadFile(configPath)
					framework.ExpectNoError(err)
					var devcontainerConfig map[string]any
					framework.ExpectNoError(json.Unmarshal(config, &devcontainerConfig))
					lifecycle, ok := devcontainerConfig["onCreateCommand"].(map[string]any)
					gomega.Expect(ok).To(gomega.BeTrue())
					lifecycle["keep"] = "echo USER_HOOK_V2 >> /workspaces/keep.log"
					delete(lifecycle, "removed")
					updated, err := json.MarshalIndent(devcontainerConfig, "", "  ")
					framework.ExpectNoError(err)
					framework.ExpectNoError(os.WriteFile(configPath, updated, 0o600))

					upArgs := append([]string{tempDir}, args...)
					err = tc.f.DevsyUp(ctx, upArgs...)
					framework.ExpectNoError(err)

					after, err := tc.getAppContainer(ctx, workspace)
					framework.ExpectNoError(err)
					metadata := after.Config.Labels[pkgconfig.DevcontainerMetadataLabel]
					gomega.Expect(metadata).To(gomega.ContainSubstring("USER_HOOK_V2"))
					gomega.Expect(metadata).NotTo(gomega.ContainSubstring("USER_HOOK_V1"))
					gomega.Expect(metadata).NotTo(gomega.ContainSubstring("REMOVED_USER_HOOK"))
					gomega.Expect(metadata).To(gomega.ContainSubstring("IMAGE_LIFECYCLE_HOOK"))
					gomega.Expect(metadata).To(gomega.ContainSubstring("STABLE_USER_HOOK"))
					gomega.Expect(after.ID).
						NotTo(gomega.Equal(before.ID), "recreation should replace the container")
					gomega.Expect(after.Config.Labels[pkgconfig.ComposeProjectLabel]).
						To(gomega.Equal(initialProject), "recreation should keep the Compose project")

					for path, expected := range map[string]string{
						"keep.log":    "USER_HOOK_V1\nUSER_HOOK_V2\n",
						"stable.log":  "STABLE_USER_HOOK\nSTABLE_USER_HOOK\n",
						"removed.log": "REMOVED_USER_HOOK\n",
						"image.log":   "IMAGE_LIFECYCLE_HOOK\nIMAGE_LIFECYCLE_HOOK\n",
					} {
						// #nosec G304 -- path is a fixed marker filename under the temporary workspace.
						contents, err := os.ReadFile(filepath.Join(tempDir, path))
						framework.ExpectNoError(err)
						gomega.Expect(string(contents)).To(gomega.Equal(expected), path)
					}
				},
				ginkgo.SpecTimeout(framework.TimeoutVeryLong()),
			)
		}
	},
)
