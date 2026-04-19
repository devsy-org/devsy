package up

import (
	"context"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
)

var _ = ginkgo.Describe(
	"testing up command for working with git repositories",
	ginkgo.Label("up-git-repositories"),
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It(
			"should allow checkout of a GitRepo from a commit hash",
			func(ctx context.Context) {
				f, err := setupDockerProvider(initialDir+"/bin", "docker")
				framework.ExpectNoError(err)

				name := "sha256-0c1547c"
				ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, name)

				// Wait for devsy workspace to come online (deadline: 30s)
				err = f.DevsyUp(
					ctx,
					"github.com/microsoft/vscode-remote-try-python@sha256:0c1547c",
				)
				framework.ExpectNoError(err)
			},
			ginkgo.SpecTimeout(framework.GetTimeout()),
		)

		ginkgo.It(
			"should allow checkout of a GitRepo from a pull request reference",
			func(ctx context.Context) {
				f, err := setupDockerProvider(initialDir+"/bin", "docker")
				framework.ExpectNoError(err)

				name := "devsy"
				ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, name)

				err = f.DevsyUp(ctx, "github.com/devsy-org/devsy@pull/1/head")
				framework.ExpectNoError(err)
			},
			ginkgo.SpecTimeout(framework.GetTimeout()*3),
		)

		ginkgo.It("create workspace in a subpath", func(ctx context.Context) {
			const providerName = "test-docker"

			f := framework.NewDefaultFramework(initialDir + "/bin")

			// provider add, use and delete afterwards
			err := f.DevsyProviderAdd(ctx, "docker", "--name", providerName)
			framework.ExpectNoError(err)
			err = f.DevsyProviderUse(ctx, providerName)
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
				err := f.DevsyProviderDelete(cleanupCtx, providerName)
				framework.ExpectNoError(err)
			})

			err = f.DevsyUp(
				ctx,
				"https://github.com/loft-sh/examples@subpath:/devsy/jupyter-notebook-hello-world",
			)
			framework.ExpectNoError(err)

			id := "subpath--devsy-jupyter-notebook-hello-world"
			out, err := f.DevsySSH(ctx, id, "pwd")
			framework.ExpectNoError(err)
			framework.ExpectEqual(out, fmt.Sprintf("/workspaces/%s\n", id), "should be subpath")

			err = f.DevsyWorkspaceDelete(ctx, id)
			framework.ExpectNoError(err)
		}, ginkgo.SpecTimeout(framework.GetTimeout()))
	},
)
