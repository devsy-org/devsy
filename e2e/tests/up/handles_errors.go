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
	"testing up command that handles workspace errors",
	ginkgo.Label("up-handle-errors"),
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It(
			"make sure devsy output is correct and log-output works correctly",
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")
				tempDir, err := framework.CopyToTempDir("tests/up/testdata/docker")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				err = f.DevsyProviderAdd(ctx, "docker", "--name", "test-docker")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					err := f.DevsyProviderDelete(cleanupCtx, "test-docker")
					framework.ExpectNoError(err)
				})

				err = f.DevsyProviderUse(
					ctx,
					"test-docker",
					"-o",
					"DOCKER_PATH=abc",
					"--skip-init",
				)
				framework.ExpectNoError(err)

				// Wait for devsy workspace to come online
				stdout, stderr, err := f.DevsyUpStreams(ctx, tempDir, "--log-output=json")
				deleteErr := f.DevsyWorkspaceDelete(ctx, tempDir, "--force")
				framework.ExpectNoError(deleteErr)
				framework.ExpectError(err, "expected error")
				framework.ExpectNoError(verifyLogStream(strings.NewReader(stdout)))
				framework.ExpectNoError(verifyLogStream(strings.NewReader(stderr)))
				framework.ExpectNoError(
					findMessage(
						strings.NewReader(stderr),
						"exec: \"abc\": executable file not found in $PATH",
					),
				)
			},
			ginkgo.SpecTimeout(framework.GetTimeout()),
		)

		ginkgo.It(
			"ensure workspace cleanup when failing to create a workspace",
			func(ctx context.Context) {
				f, err := setupDockerProvider(initialDir+"/bin", "docker")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyProviderDelete(cleanupCtx, "docker")
				})

				initialList, err := f.DevsyList(ctx)
				framework.ExpectNoError(err)
				// Wait for devsy workspace to come online (deadline: 30s)
				err = f.DevsyUp(ctx, "github.com/i/do-not-exist.git")
				framework.ExpectError(err)

				out, err := f.DevsyList(ctx)
				framework.ExpectNoError(err)
				framework.ExpectEqual(out, initialList)
			},
			ginkgo.SpecTimeout(framework.GetTimeout()),
		)

		ginkgo.It(
			"should fail with error when bind mount source does not exist",
			func(ctx context.Context) {
				f, err := setupDockerProvider(initialDir+"/bin", "docker")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyProviderDelete(cleanupCtx, "docker")
				})

				tempDir, err := framework.CopyToTempDir(
					"tests/up/testdata/docker-invalid-bind-mount",
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				err = f.DevsyUp(ctx, tempDir, "--debug")

				gomega.Expect(err).To(gomega.HaveOccurred())
				gomega.Expect(err.Error()).To(gomega.ContainSubstring("devsy up failed"))
				gomega.Expect(err.Error()).To(gomega.ContainSubstring("exit status 1"))
			},
			ginkgo.SpecTimeout(framework.GetTimeout()),
		)

		ginkgo.It("ensure workspace cleanup when not a git or folder", func(ctx context.Context) {
			f, err := setupDockerProvider(initialDir+"/bin", "docker")
			framework.ExpectNoError(err)

			initialList, err := f.DevsyList(ctx)
			framework.ExpectNoError(err)
			// Wait for devsy workspace to come online (deadline: 30s)
			err = f.DevsyUp(ctx, "notfound.devsy.sh")
			framework.ExpectError(err)

			out, err := f.DevsyList(ctx)
			framework.ExpectNoError(err)
			framework.ExpectEqual(out, initialList)
		}, ginkgo.SpecTimeout(framework.GetTimeout()))
	},
)
