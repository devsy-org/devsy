package ide

import (
	"context"
	"os"

	"github.com/onsi/ginkgo/v2"
	"github.com/devsy-org/devsy/e2e/framework"
)

var _ = ginkgo.Describe("devsy ide test suite", ginkgo.Label("ide"), ginkgo.Ordered, func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.It("start ides", ginkgo.SpecTimeout(framework.GetTimeout()), func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")
		tempDir, err := framework.CopyToTempDir("tests/ide/testdata")
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

		err = f.DevsyProviderAdd(ctx, "docker")
		framework.ExpectNoError(err)
		err = f.DevsyProviderUse(ctx, "docker")
		framework.ExpectNoError(err)

		ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
			err = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
			framework.ExpectNoError(err)
		})

		err = f.DevsyUpWithIDE(ctx, tempDir, "--open-ide=false", "--ide=vscode")
		framework.ExpectNoError(err)

		err = f.DevsyUpWithIDE(ctx, tempDir, "--open-ide=false", "--ide=openvscode")
		framework.ExpectNoError(err)

		err = f.DevsyUpWithIDE(ctx, tempDir, "--open-ide=false", "--ide=jupyternotebook")
		framework.ExpectNoError(err)

		// TODO: Fix broken IDE
		// err = f.DevsyUpWithIDE(ctx, tempDir, "--open-ide=false", "--ide=fleet")
		// framework.ExpectNoError(err)

		// check if ssh works
		err = f.DevsySSHEchoTestString(ctx, tempDir)
		framework.ExpectNoError(err)

		// TODO: test jetbrains ides
	})
})
