package up

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/docker"
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

			//nolint:dupl // shared rootful podman wrapper setup across split files
			ginkgo.BeforeEach(func(ctx context.Context) {
				wrapper, err := os.Create( //nolint:gosec // G304: test-controlled path
					initialDir + "/bin/podman-rootful",
				)
				framework.ExpectNoError(err)

				_, err = wrapper.WriteString("#!/bin/sh\nsudo podman \"$@\"\n")
				if err != nil {
					_ = wrapper.Close()
					framework.ExpectNoError(err)
				}

				err = wrapper.Close()
				framework.ExpectNoError(err)

				// #nosec G302 -- wrapper script needs execute permission
				err = os.Chmod(initialDir+"/bin/podman-rootful", 0o755)
				framework.ExpectNoError(err)

				cmd := exec.CommandContext( //nolint:gosec // G204: test-controlled path
					ctx, initialDir+"/bin/podman-rootful", "ps",
				)
				docker.PrepareForGroupCancellation(cmd)
				out, err := cmd.CombinedOutput()
				framework.ExpectNoError(err, string(out))

				ginkgo.DeferCleanup(func() {
					_ = os.Remove(initialDir + "/bin/podman-rootful")
				})

				f, err = setupDockerProvider(
					initialDir+"/bin",
					initialDir+"/bin/podman-rootful",
				)
				framework.ExpectNoError(err)
			},
			)

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
