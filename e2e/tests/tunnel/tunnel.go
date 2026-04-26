package tunnel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"devsy tunnel test suite",
	ginkgo.Label("tunnel"),
	ginkgo.Ordered,
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It("should tunnel to workspace and exchange data bidirectionally",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")

				tempDir, err := framework.CopyToTempDirWithoutChdir(
					initialDir + "/tests/tunnel/testdata/tunnel",
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				_ = f.DevsyProviderDelete(ctx, "docker123")
				err = f.DevsyProviderAdd(ctx, filepath.Join(tempDir, "provider.yaml"))
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					err = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.ExpectNoError(err)
					err = f.DevsyProviderDelete(cleanupCtx, "docker123")
					framework.ExpectNoError(err)
				})

				err = f.DevsyUp(ctx, tempDir, "--debug")
				framework.ExpectNoError(err)

				out, err := f.DevsySSH(
					ctx,
					tempDir,
					"echo TestTunnelData123 | cat",
				)
				framework.ExpectNoError(err)
				gomega.Expect(out).To(
					gomega.ContainSubstring("TestTunnelData123"),
					"bidirectional tunnel data exchange failed",
				)

				status, err := f.DevsyStatus(ctx, tempDir, "--container-status=false")
				framework.ExpectNoError(err)
				framework.ExpectEqual(
					strings.ToUpper(status.State),
					"RUNNING",
					"workspace should remain running after SSH session",
				)
			})

		ginkgo.It("should keep workspace running when stdin closes before stdout",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")

				tempDir, err := framework.CopyToTempDirWithoutChdir(
					initialDir + "/tests/tunnel/testdata/tunnel",
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				_ = f.DevsyProviderDelete(ctx, "docker123")
				err = f.DevsyProviderAdd(ctx, filepath.Join(tempDir, "provider.yaml"))
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					err = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.ExpectNoError(err)
					err = f.DevsyProviderDelete(cleanupCtx, "docker123")
					framework.ExpectNoError(err)
				})

				err = f.DevsyUp(ctx, tempDir, "--debug")
				framework.ExpectNoError(err)

				out, err := f.DevsySSH(ctx, tempDir, "echo alive")
				framework.ExpectNoError(err)
				gomega.Expect(out).To(
					gomega.ContainSubstring("alive"),
					"SSH command should produce output",
				)

				// Verify workspace stays running after stdin pipe closes.
				gomega.Consistently(func() string {
					status, err := f.DevsyStatus(ctx, tempDir, "--container-status=false")
					framework.ExpectNoError(err)
					return strings.ToUpper(status.State)
				}, 15*time.Second, 2*time.Second).Should(
					gomega.Equal("RUNNING"),
					"workspace must stay running after stdin closes",
				)
			})
	},
)
