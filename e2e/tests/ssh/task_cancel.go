package ssh

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/command"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

// stoppedHoldWindow outlasts the tunnel health-check interval and failure
// threshold (30s x 3), so a workspace still Stopped after the window was not
// restarted by the health loop.
const stoppedHoldWindow = 100 * time.Second

var _ = ginkgo.Describe(
	"devsy workspace task cancel",
	ginkgo.Label("task-cancel"),
	ginkgo.Ordered,
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It("should cancel a detached tunnel task and keep the workspace stopped",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				tempDir, err := framework.CopyToTempDir("tests/ssh/testdata/local-test")
				framework.ExpectNoError(err)

				f := setupTunnelProvider(ctx, initialDir)

				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.CleanupTempDir(initialDir, tempDir)
				})

				taskID, err := startDetachedTunnelUp(ctx, f, tempDir)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceTaskCancel(cleanupCtx, taskID)
				})

				active := waitDetachedTunnelReady(ctx, f, taskID)
				gomega.Expect(active.PID).NotTo(gomega.BeZero(),
					"detached up worker should publish its pid")

				// Cancellation goes through the product task API; on Windows
				// this used to panic with "unsupported".
				framework.ExpectNoError(f.DevsyWorkspaceTaskCancel(ctx, taskID))

				canceled := waitDetachedTaskCanceled(ctx, f, taskID)
				gomega.Expect(canceled.Error).To(gomega.Equal("canceled"))

				gomega.Eventually(func() bool {
					running, err := command.IsRunning(strconv.Itoa(active.PID))
					return err == nil && !running
				}).WithContext(ctx).WithTimeout(30*time.Second).WithPolling(time.Second).
					Should(gomega.BeTrue(),
						"detached worker process should be gone after cancel")

				framework.ExpectNoError(f.DevsyStop(ctx, tempDir))

				expectWorkspaceStaysStopped(ctx, f, tempDir)
			},
		)

		ginkgo.It("should quiesce a persisted detached task when stop runs without a task ID",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				tempDir, err := framework.CopyToTempDir("tests/ssh/testdata/local-test")
				framework.ExpectNoError(err)

				f := setupTunnelProvider(ctx, initialDir)

				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.CleanupTempDir(initialDir, tempDir)
				})

				taskID, err := startDetachedTunnelUp(ctx, f, tempDir)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceTaskCancel(cleanupCtx, taskID)
				})

				waitDetachedTunnelReady(ctx, f, taskID)

				// Stop discovers the persisted task on its own and cancels it.
				framework.ExpectNoError(f.DevsyStop(ctx, tempDir))

				canceled := waitDetachedTaskCanceled(ctx, f, taskID)
				gomega.Expect(canceled.Error).To(gomega.Equal("canceled"))

				expectWorkspaceStaysStopped(ctx, f, tempDir)
			},
		)
	},
)

func expectWorkspaceStaysStopped(ctx context.Context, f *framework.Framework, workspace string) {
	gomega.Consistently(func() string {
		status, err := f.DevsyStatus(ctx, workspace)
		if err != nil {
			return "status error: " + err.Error()
		}
		return status.State
	}).WithContext(ctx).WithTimeout(stoppedHoldWindow).WithPolling(10 * time.Second).
		Should(gomega.Equal(client.StatusStopped))
}
