package ide

import (
	"context"
	"os"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("devsy up --ide-launch=skip", ginkgo.Label("ide"), ginkgo.Ordered, func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.It("does not install an IDE server binary when --ide is omitted",
		func(ctx context.Context) {
			f, tempDir := setupBrowserIDE(ctx, initialDir)

			err := f.DevsyUpWithIDE(ctx, "--ide-launch=skip", tempDir)
			framework.ExpectNoError(err)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"workspace", "exec", "--workspace-folder", tempDir,
				"--", "sh", "-c", "test -d /root/.openvscode-server && echo present || echo absent",
			})
			framework.ExpectNoError(err)
			gomega.Expect(stdout).To(gomega.ContainSubstring("absent"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))
})
