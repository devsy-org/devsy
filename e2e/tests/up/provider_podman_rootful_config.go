package up

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"testing up command for podman provider",
	ginkgo.Label("up-provider-podman-rootful-config"),
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.Context("with rootful podman", func() {
			var f *framework.Framework

			ginkgo.BeforeEach(func(ctx context.Context) {
				f = setupRootfulPodman(ctx, initialDir)
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

					devContainerID := eventuallySSH(
						f, ctx, tempDir, "cat $HOME/dev-container-id.out",
					)
					gomega.Expect(devContainerID).NotTo(gomega.BeEmpty())

					containerEnvPath := eventuallySSH(
						f, ctx, tempDir, "cat $HOME/container-env-path.out",
					)
					gomega.Expect(containerEnvPath).To(gomega.ContainSubstring("/usr/local/bin"))

					localEnvHome := eventuallySSH(f, ctx, tempDir, "cat $HOME/local-env-home.out")
					gomega.Expect(localEnvHome).
						To(gomega.Equal(os.Getenv("HOME")))

					localWorkspaceFolder := eventuallySSH(
						f, ctx, tempDir, "cat $HOME/local-workspace-folder.out",
					)
					gomega.Expect(
						framework.CleanString(localWorkspaceFolder),
					).To(gomega.Equal(framework.CleanString(tempDir)))

					localWorkspaceFolderBasename := eventuallySSH(
						f, ctx, tempDir, "cat $HOME/local-workspace-folder-basename.out",
					)
					gomega.Expect(localWorkspaceFolderBasename).
						To(gomega.Equal(filepath.Base(tempDir)))

					containerWorkspaceFolder := eventuallySSH(
						f, ctx, tempDir, "cat $HOME/container-workspace-folder.out",
					)
					gomega.Expect(
						framework.CleanString(containerWorkspaceFolder),
					).To(gomega.Equal(
						framework.CleanString(path.Join("/workspaces", filepath.Base(tempDir))),
					))

					containerWorkspaceFolderBasename := eventuallySSH(
						f, ctx, tempDir, "cat $HOME/container-workspace-folder-basename.out",
					)
					gomega.Expect(containerWorkspaceFolderBasename).
						To(gomega.Equal(filepath.Base(tempDir)))

					customVar := eventuallySSH(f, ctx, tempDir, "cat $HOME/custom-var.out")
					gomega.Expect(customVar).To(gomega.Equal("custom_value"))

					customImage := eventuallySSH(f, ctx, tempDir, "cat $HOME/custom-image.out")
					gomega.Expect(customImage).
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

					gomega.Eventually(func() string {
						out, err := probeSSH(
							f, ctx, tempDir, "bash -l -c 'echo -n $BASE_VAR'",
						)
						if err != nil {
							return ""
						}
						return strings.TrimSpace(out)
					}).WithTimeout(60 * time.Second).WithPolling(2 * time.Second).Should(
						gomega.Equal("base_value"),
					)

					gomega.Eventually(func() string {
						out, err := probeSSH(
							f, ctx, tempDir, "bash -l -c 'echo -n $EXTRA_VAR'",
						)
						if err != nil {
							return ""
						}
						return strings.TrimSpace(out)
					}).WithTimeout(60 * time.Second).WithPolling(2 * time.Second).Should(
						gomega.Equal("extra_value"),
					)

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
			})
		})
	},
)

func eventuallySSH(f *framework.Framework, ctx context.Context, workspace, command string) string {
	var output string
	gomega.Eventually(func() bool {
		probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		out, err := f.DevsySSHOnce(probeCtx, workspace, command)
		if err != nil {
			return false
		}
		output = strings.TrimSpace(out)
		return true
	}).WithTimeout(60 * time.Second).WithPolling(2 * time.Second).Should(gomega.BeTrue())
	return output
}
