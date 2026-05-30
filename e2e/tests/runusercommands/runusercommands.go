package runusercommands

import (
	"context"
	"os"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"devsy run-user-commands test suite",
	ginkgo.Label("run-user-commands"),
	ginkgo.Ordered,
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It("should execute lifecycle hooks via run-user-commands",
			func(ctx context.Context) {
				tempDir, f, err := setupWorkspaceAndUp(
					ctx,
					"tests/runusercommands/testdata/lifecycle",
					initialDir,
				)
				framework.ExpectNoError(err)

				_, _, err = f.ExecCommandCapture(ctx, []string{
					"internal", "run-user-commands",
					"--workspace-folder", tempDir,
				})
				framework.ExpectNoError(err)

				stdout, _, err := f.ExecCommandCapture(ctx, []string{
					"exec",
					"--workspace-folder", tempDir,
					"--", "cat", "/tmp/lifecycle-test.txt",
				})
				framework.ExpectNoError(err)
				gomega.Expect(strings.TrimSpace(stdout)).To(gomega.ContainSubstring("oncreate-ran"))
				gomega.Expect(strings.TrimSpace(stdout)).
					To(gomega.ContainSubstring("postcreate-ran"))
			}, ginkgo.SpecTimeout(framework.TimeoutShort()))
	},
)
