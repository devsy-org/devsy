//go:build !windows

package ide

import (
	"context"
	"os"
	"strings"
	"syscall"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/ide/opener"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"devsy up browser IDE returns instead of blocking",
	ginkgo.Label("ide"),
	ginkgo.Ordered,
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It(
			"spawns a detached browser tunnel and returns",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")
				tempDir, err := framework.CopyToTempDir("tests/ide/testdata")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				err = f.DevsyProviderAdd(ctx, "docker")
				framework.ExpectNoError(err)
				err = f.DevsyProviderUse(ctx, "docker")
				framework.ExpectNoError(err)

				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					err := f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.ExpectNoError(err)
				})

				// Run up with a browser IDE. --ide-option OPEN=false suppresses the
				// host browser launch (we'd have nothing to display it on in CI) but
				// still runs openIDE → startDetachedBrowserTunnel → writes tunnel.json.
				// --open-ide=false would skip openIDE entirely, which is not what we
				// want to exercise. With the old blocking behavior this would still
				// hang past SpecTimeout; with the new behavior the CLI returns.
				err = f.DevsyUpWithIDE(
					ctx,
					tempDir,
					"--ide=openvscode",
					"--ide-option",
					"OPEN=false",
				)
				framework.ExpectNoError(err)

				// Resolve the workspace and read its tunnel.json.
				ws, err := f.FindWorkspace(ctx, tempDir)
				framework.ExpectNoError(err)
				gomega.Expect(ws).NotTo(gomega.BeNil())
				gomega.Expect(ws.Context).NotTo(gomega.BeEmpty())

				state, err := opener.ReadTunnelState(ws.Context, ws.ID)
				framework.ExpectNoError(err)
				gomega.Expect(state).
					NotTo(gomega.BeNil(), "expected tunnel.json to exist for browser IDE")
				gomega.Expect(state.PID).To(gomega.BeNumerically(">", 0))
				gomega.Expect(state.Label).To(gomega.Equal("vscode"))
				gomega.Expect(strings.HasPrefix(state.TargetURL, "http://localhost:")).
					To(gomega.BeTrue(), "expected TargetURL to start with http://localhost:, got %s", state.TargetURL)

				// Verify the helper PID is alive: signal 0 returns nil if the process exists.
				err = syscall.Kill(state.PID, 0)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(),
					"expected helper PID %d to be alive", state.PID)

				// Stop the workspace; the tunnel state should be cleaned up.
				err = f.DevsyStop(ctx, tempDir)
				framework.ExpectNoError(err)

				stateAfter, err := opener.ReadTunnelState(ws.Context, ws.ID)
				framework.ExpectNoError(err)
				gomega.Expect(stateAfter).To(gomega.BeNil(),
					"expected tunnel.json to be removed after devsy stop")
			},
		)
	},
)
