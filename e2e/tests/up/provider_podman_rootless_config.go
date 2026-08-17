package up

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
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
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

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
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

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
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

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
					ginkgo.SpecTimeout(framework.TimeoutShort()),
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
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))
			})
		})
	},
)
