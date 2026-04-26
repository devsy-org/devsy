package machineprovider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"devsy machine provider test suite",
	ginkgo.Label("machineprovider"),
	ginkgo.Ordered,
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It("test start / stop / status",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")

				// copy test dir
				tempDir, err := framework.CopyToTempDirWithoutChdir(
					initialDir + "/tests/machineprovider/testdata/machineprovider",
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				// create docker provider
				err = f.DevsyProviderAdd(
					ctx,
					filepath.Join(tempDir, "provider.yaml"),
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					err = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.ExpectNoError(err)
				})

				// wait for devsy workspace to come online (deadline: 30s)
				err = f.DevsyUp(ctx, tempDir, "--debug")
				framework.ExpectNoError(err)

				// expect workspace
				workspace, err := f.FindWorkspace(ctx, tempDir)
				framework.ExpectNoError(err)

				// check status
				status, err := f.DevsyStatus(ctx, tempDir)
				framework.ExpectNoError(err)
				framework.ExpectEqual(
					strings.ToUpper(status.State),
					"RUNNING",
					"workspace status did not match",
				)

				// stop container
				err = f.DevsyStop(ctx, tempDir)
				framework.ExpectNoError(err)

				// check status
				status, err = f.DevsyStatus(ctx, tempDir)
				framework.ExpectNoError(err)
				framework.ExpectEqual(
					strings.ToUpper(status.State),
					"STOPPED",
					"workspace status did not match",
				)

				// wait for devsy workspace to come online (deadline: 30s)
				err = f.DevsyUp(ctx, tempDir)
				framework.ExpectNoError(err)

				// check if ssh works as it should start the container
				out, err := f.DevsySSH(
					ctx,
					tempDir,
					fmt.Sprintf("cat /workspaces/%s/test.txt", workspace.ID),
				)
				framework.ExpectNoError(err)
				framework.ExpectEqual(
					strings.TrimSpace(out),
					"Test123",
					"workspace content does not match",
				)
			})

		ginkgo.It("test devsy inactivity timeout",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")

				// copy test dir
				tempDir, err := framework.CopyToTempDirWithoutChdir(
					initialDir + "/tests/machineprovider/testdata/machineprovider2",
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				// create provider
				_ = f.DevsyProviderDelete(ctx, "docker123")
				err = f.DevsyProviderAdd(ctx, filepath.Join(tempDir, "provider.yaml"))
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					err = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.ExpectNoError(err)
					err = f.DevsyProviderDelete(cleanupCtx, "docker123")
					framework.ExpectNoError(err)
				})

				// wait for devsy workspace to come online (deadline: 30s)
				err = f.DevsyUp(ctx, tempDir, "--debug", "--daemon-interval=3s")
				framework.ExpectNoError(err)

				// check status
				status, err := f.DevsyStatus(ctx, tempDir, "--container-status=false")
				framework.ExpectNoError(err)
				framework.ExpectEqual(
					strings.ToUpper(status.State),
					"RUNNING",
					"workspace status did not match",
				)

				// stop container
				err = f.DevsyStop(ctx, tempDir)
				framework.ExpectNoError(err)

				// check status
				status, err = f.DevsyStatus(ctx, tempDir, "--container-status=false")
				framework.ExpectNoError(err)
				framework.ExpectEqual(
					strings.ToUpper(status.State),
					"STOPPED",
					"workspace status did not match",
				)

				// wait for devsy workspace to come online (deadline: 30s)
				err = f.DevsyUp(ctx, tempDir, "--daemon-interval=3s")
				framework.ExpectNoError(err)

				// check status
				status, err = f.DevsyStatus(ctx, tempDir, "--container-status=false")
				framework.ExpectNoError(err)
				framework.ExpectEqual(
					strings.ToUpper(status.State),
					"RUNNING",
					"workspace status did not match",
				)

				// wait until workspace is stopped again
				gomega.Eventually(func() string {
					status, err := f.DevsyStatus(ctx, tempDir, "--container-status=false")
					framework.ExpectNoError(err)
					return strings.ToUpper(status.State)
				}, time.Minute*2, time.Second*2).Should(
					gomega.Equal("STOPPED"),
					"machine did not shutdown in time",
				)
			})

		ginkgo.It("test shutdownAction none suppresses inactivity timeout",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")

				// copy test dir — uses devcontainer.json with shutdownAction: "none"
				tempDir, err := framework.CopyToTempDirWithoutChdir(
					initialDir + "/tests/machineprovider/testdata/machineprovider3",
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				// create provider (same 5s inactivity timeout as machineprovider2)
				_ = f.DevsyProviderDelete(ctx, "docker123")
				err = f.DevsyProviderAdd(ctx, filepath.Join(tempDir, "provider.yaml"))
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					err = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.ExpectNoError(err)
					err = f.DevsyProviderDelete(cleanupCtx, "docker123")
					framework.ExpectNoError(err)
				})

				// wait for devsy workspace to come online
				err = f.DevsyUp(ctx, tempDir, "--debug", "--daemon-interval=3s")
				framework.ExpectNoError(err)

				// check initial status
				status, err := f.DevsyStatus(ctx, tempDir, "--container-status=false")
				framework.ExpectNoError(err)
				framework.ExpectEqual(
					strings.ToUpper(status.State),
					"RUNNING",
					"workspace status did not match",
				)

				// stop and restart to trigger timeout monitor path
				err = f.DevsyStop(ctx, tempDir)
				framework.ExpectNoError(err)

				err = f.DevsyUp(ctx, tempDir, "--daemon-interval=3s")
				framework.ExpectNoError(err)

				// verify workspace stays running well past the 5s timeout.
				// The timeout would fire within ~15s (5s timeout + 10s ticker).
				// We assert RUNNING for 30s to give ample margin.
				gomega.Consistently(func() string {
					status, err := f.DevsyStatus(ctx, tempDir, "--container-status=false")
					framework.ExpectNoError(err)
					return strings.ToUpper(status.State)
				}, 30*time.Second, 2*time.Second).Should(
					gomega.Equal("RUNNING"),
					"workspace should stay running when shutdownAction is none",
				)
			})
	},
)
