package up

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	docker "github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"testing up command for podman provider",
	ginkgo.Label("up-provider-podman-rootless-config"),
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.Context("with rootless podman", func() {
			var f *framework.Framework

			ginkgo.BeforeEach(func(ctx context.Context) {
				var err error
				f, err = setupDockerProvider(initialDir+"/bin", "podman")
				framework.ExpectNoError(err)
			})

			ginkgo.Context("configuration", func() { //nolint:dupl
				ginkgo.It("should substitute variables", func(ctx context.Context) {
					tempDir, err := setupWorkspaceAndUp(
						ctx,
						"tests/up/testdata/docker-variables",
						initialDir,
						f,
						"--init-env", "CUSTOM_VAR=custom_value",
						"--init-env", "CUSTOM_IMAGE=ghcr.io/devsy-org/test-images/base:alpine",
					)
					framework.ExpectNoError(err)

					devContainerID, err := f.DevsySSH(
						ctx,
						tempDir,
						"cat $HOME/dev-container-id.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(devContainerID)).NotTo(gomega.BeEmpty())

					containerEnvPath, err := f.DevsySSH(
						ctx, tempDir, "cat $HOME/container-env-path.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(containerEnvPath).To(gomega.ContainSubstring("/usr/local/bin"))

					localEnvHome, err := f.DevsySSH(ctx, tempDir, "cat $HOME/local-env-home.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(localEnvHome)).
						To(gomega.Equal(os.Getenv("HOME")))

					localWorkspaceFolder, err := f.DevsySSH(
						ctx, tempDir, "cat $HOME/local-workspace-folder.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(
						framework.CleanString(strings.TrimSpace(localWorkspaceFolder)),
					).To(gomega.Equal(framework.CleanString(tempDir)))

					localWorkspaceFolderBasename, err := f.DevsySSH(
						ctx, tempDir, "cat $HOME/local-workspace-folder-basename.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(localWorkspaceFolderBasename)).
						To(gomega.Equal(filepath.Base(tempDir)))

					containerWorkspaceFolder, err := f.DevsySSH(
						ctx, tempDir, "cat $HOME/container-workspace-folder.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(
						framework.CleanString(strings.TrimSpace(containerWorkspaceFolder)),
					).To(gomega.Equal(
						framework.CleanString(path.Join("/workspaces", filepath.Base(tempDir))),
					))

					containerWorkspaceFolderBasename, err := f.DevsySSH(
						ctx, tempDir, "cat $HOME/container-workspace-folder-basename.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(containerWorkspaceFolderBasename)).
						To(gomega.Equal(filepath.Base(tempDir)))

					customVar, err := f.DevsySSH(ctx, tempDir, "cat $HOME/custom-var.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(customVar)).To(gomega.Equal("custom_value"))

					customImage, err := f.DevsySSH(ctx, tempDir, "cat $HOME/custom-image.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(customImage)).
						To(gomega.Equal("ghcr.io/devsy-org/test-images/base:alpine"))
				}, ginkgo.SpecTimeout(framework.TimeoutModerate()))

				ginkgo.It("should substitute variables with defaults", func(ctx context.Context) {
					tempDir, err := setupWorkspaceAndUp(
						ctx,
						"tests/up/testdata/docker-variables-defaults",
						initialDir,
						f,
					)
					framework.ExpectNoError(err)

					withDefault, err := f.DevsySSH(ctx, tempDir, "cat $HOME/with-default.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(withDefault)).
						To(gomega.Equal("my_default_value"))

					colonDefault, err := f.DevsySSH(ctx, tempDir, "cat $HOME/colon-default.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(colonDefault)).
						To(gomega.Equal("http://proxy:8080"))

					setVar, err := f.DevsySSH(ctx, tempDir, "cat $HOME/set-var.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(setVar)).To(gomega.Equal(os.Getenv("HOME")))
				}, ginkgo.SpecTimeout(framework.TimeoutModerate()))

				ginkgo.It("should merge extra devcontainer config", func(ctx context.Context) {
					tempDir, err := setupWorkspace(
						"tests/up/testdata/docker-extra-devcontainer",
						initialDir,
						f,
					)
					framework.ExpectNoError(err)

					extraPath := path.Join(tempDir, "extra.json")
					err = f.DevsyUp(
						ctx,
						tempDir,
						names.Flag(names.DevContainerOverlay),
						extraPath,
					)
					framework.ExpectNoError(err)

					out, err := f.DevsySSH(ctx, tempDir, "bash -l -c 'echo -n $BASE_VAR'")
					framework.ExpectNoError(err)
					framework.ExpectEqual(out, "base_value")

					out, err = f.DevsySSH(ctx, tempDir, "bash -l -c 'echo -n $EXTRA_VAR'")
					framework.ExpectNoError(err)
					framework.ExpectEqual(out, "extra_value")

					err = f.DevsyWorkspaceDelete(ctx, tempDir)
					framework.ExpectNoError(err)
				}, ginkgo.SpecTimeout(framework.TimeoutModerate()))

				ginkgo.It(
					"should override with extra devcontainer config",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-extra-override",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						extraPath := path.Join(tempDir, "override.json")
						err = f.DevsyUp(
							ctx,
							tempDir,
							names.Flag(names.DevContainerOverlay),
							extraPath,
						)
						framework.ExpectNoError(err)

						out, err := f.DevsySSH(ctx, tempDir, "cat /tmp/test-var.out")
						framework.ExpectNoError(err)
						framework.ExpectEqual(strings.TrimSpace(out), "overridden_value")

						err = f.DevsyWorkspaceDelete(ctx, tempDir)
						framework.ExpectNoError(err)
					},
					ginkgo.SpecTimeout(framework.TimeoutModerate()),
				)

				ginkgo.It("should select from multiple devcontainers", func(ctx context.Context) {
					tempDir, err := setupWorkspace(
						"tests/up/testdata/docker-multi-devcontainer",
						initialDir,
						f,
					)
					framework.ExpectNoError(err)

					err = f.DevsyUp(
						ctx,
						tempDir,
						names.Flag(names.DevContainer),
						"id:python",
					)
					framework.ExpectNoError(err)

					out, err := f.DevsySSH(
						ctx, tempDir, "bash -l -c 'echo -n $DEVCONTAINER_TYPE'",
					)
					framework.ExpectNoError(err)
					framework.ExpectEqual(out, "python")

					err = f.DevsyWorkspaceDelete(ctx, tempDir)
					framework.ExpectNoError(err)

					err = f.DevsyUp(ctx, tempDir, names.Flag(names.DevContainer), "id:go")
					framework.ExpectNoError(err)

					out, err = f.DevsySSH(
						ctx, tempDir, "bash -l -c 'echo -n $DEVCONTAINER_TYPE'",
					)
					framework.ExpectNoError(err)
					framework.ExpectEqual(out, "go")

					err = f.DevsyWorkspaceDelete(ctx, tempDir)
					framework.ExpectNoError(err)
				}, ginkgo.SpecTimeout(framework.TimeoutModerate()))

				ginkgo.It(
					"should preserve localEnv expressions in build metadata",
					func(ctx context.Context) {
						homeDir, err := os.UserHomeDir()
						framework.ExpectNoError(err)

						sourceDir := filepath.Join(
							homeDir,
							".devsy-e2e-local-env-metadata",
						)
						if _, statErr := os.Stat(sourceDir); statErr == nil {
							probePath := filepath.Join(sourceDir, "probe.txt")
							_, probeErr := os.Stat(probePath)
							gomega.Expect(probeErr).NotTo(
								gomega.HaveOccurred(),
								"fixture directory %s already exists without probe.txt; aborting to prevent data loss",
								sourceDir,
							)
						}

						// #nosec G301 -- fixture must be traversable by container user
						err = os.MkdirAll(
							sourceDir,
							0o755,
						)
						framework.ExpectNoError(err)

						ginkgo.DeferCleanup(func() {
							_ = os.RemoveAll(sourceDir)
						})

						// #nosec G306 -- fixture must be readable by container user
						err = os.WriteFile(
							filepath.Join(sourceDir, "probe.txt"),
							[]byte("devsy-local-env-metadata-ok\n"),
							0o644,
						)
						framework.ExpectNoError(err)

						tempDir, err := setupWorkspaceAndUp(
							ctx,
							"tests/up/testdata/podman-local-env-metadata",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						out, err := f.DevsySSH(
							ctx,
							tempDir,
							"cat /tmp/devsy-local-env-metadata/probe.txt",
						)
						framework.ExpectNoError(err)

						gomega.Expect(strings.TrimSpace(out)).
							To(gomega.Equal("devsy-local-env-metadata-ok"))

						workspace, err := f.FindWorkspace(ctx, tempDir)
						framework.ExpectNoError(err)

						dockerHelper := &docker.DockerHelper{DockerCommand: podmanBinName}

						container, err := dockerHelper.FindDevContainer(ctx, []string{
							fmt.Sprintf("%s=%s", pkgconfig.DevcontainerIDLabel, workspace.UID),
						})
						framework.ExpectNoError(err)
						gomega.Expect(container).NotTo(gomega.BeNil())

						imageRef := container.Config.LegacyImage
						if imageRef == "" {
							var rawInspect []struct {
								Image string `json:"Image"`
							}
							inspectErr := dockerHelper.Inspect(
								ctx,
								[]string{container.ID},
								"container",
								&rawInspect,
							)
							framework.ExpectNoError(inspectErr)
							if len(rawInspect) > 0 {
								imageRef = rawInspect[0].Image
							}
						}
						gomega.Expect(imageRef).NotTo(gomega.BeEmpty())

						imageDetails, err := dockerHelper.InspectImage(ctx, imageRef, false)
						framework.ExpectNoError(err)
						gomega.Expect(imageDetails.Config.Labels).NotTo(gomega.BeNil())

						metadataValue, ok := imageDetails.Config.Labels[pkgconfig.DevcontainerMetadataLabel]
						gomega.Expect(ok).To(gomega.BeTrue())
						gomega.Expect(metadataValue).NotTo(gomega.BeEmpty())

						var metadataList []*config.ImageMetadata
						err = json.Unmarshal([]byte(metadataValue), &metadataList)
						framework.ExpectNoError(err)
						gomega.Expect(metadataList).NotTo(gomega.BeEmpty())

						var foundSource string
						for _, item := range metadataList {
							for _, m := range item.Mounts {
								if strings.Contains(m.Source, ".devsy-e2e-local-env-metadata") {
									foundSource = m.Source
									break
								}
							}
						}
						gomega.Expect(foundSource).
							To(gomega.Equal("${localEnv:HOME}/.devsy-e2e-local-env-metadata"))

						gomega.Expect(metadataValue).To(
							gomega.ContainSubstring(
								"${localEnv:HOME}/.devsy-e2e-local-env-metadata",
							),
						)
						gomega.Expect(metadataValue).NotTo(
							gomega.ContainSubstring(
								`\${localEnv:HOME}/.devsy-e2e-local-env-metadata`,
							),
						)
						gomega.Expect(metadataValue).NotTo(
							gomega.ContainSubstring(sourceDir),
						)
					},
					ginkgo.SpecTimeout(framework.TimeoutModerate()),
				)
			})
		})
	},
)
