package logs

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"devsy logs test suite",
	ginkgo.Label("logs"),
	ginkgo.Ordered,
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It("should return workspace container logs",
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

				status, err := f.DevsyStatus(ctx, tempDir, "--container-status=false")
				framework.ExpectNoError(err)
				framework.ExpectEqual(
					strings.ToUpper(status.State),
					"RUNNING",
					"workspace should be running before fetching logs",
				)

				out, err := f.DevsyLogs(ctx, tempDir)
				framework.ExpectNoError(err)
				gomega.Expect(out).ToNot(
					gomega.BeEmpty(),
					"devsy logs should return non-empty output for a running workspace",
				)
			})
	},
)
