package up

import (
	"context"
	"os"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"testing up command for podman provider",
	ginkgo.Label("up-provider-podman-rootful-features"),
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

			ginkgo.Context("features", func() { //nolint:dupl
				ginkgo.It("should mount volumes", func(ctx context.Context) {
					tempDir, err := setupWorkspaceAndUp(
						ctx,
						"tests/up/testdata/docker-mounts",
						initialDir,
						f,
						"--debug",
					)
					framework.ExpectNoError(err)

					foo, err := f.DevsySSH(ctx, tempDir, "cat $HOME/mnt1/foo.txt")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(foo)).To(gomega.Equal("BAR"))

					bar, err := f.DevsySSH(ctx, tempDir, "cat $HOME/mnt2/bar.txt")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(bar)).To(gomega.Equal("FOO"))
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

				ginkgo.It("should use custom image", func(ctx context.Context) {
					tempDir, err := setupWorkspaceAndUp(
						ctx,
						"tests/up/testdata/docker",
						initialDir,
						f,
						"--devcontainer-image",
						"ghcr.io/devsy-org/test-images/base:alpine",
					)
					framework.ExpectNoError(err)

					out, err := f.DevsySSH(ctx, tempDir, "grep ^ID= /etc/os-release")
					framework.ExpectNoError(err)
					framework.ExpectEqual(out, "ID=alpine\n")
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

				ginkgo.It("should skip build with custom image", func(ctx context.Context) {
					tempDir, err := setupWorkspaceAndUp(
						ctx,
						"tests/up/testdata/docker-with-multi-stage-build",
						initialDir,
						f,
						"--devcontainer-image",
						"ghcr.io/devsy-org/test-images/base:alpine",
					)
					framework.ExpectNoError(err)

					out, err := f.DevsySSH(ctx, tempDir, "grep ^ID= /etc/os-release")
					framework.ExpectNoError(err)
					framework.ExpectEqual(out, "ID=alpine\n")
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))
			})
		})
	},
)
