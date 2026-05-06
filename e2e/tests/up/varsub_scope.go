package up

import (
	"context"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"testing variable substitution phase-aware scoping",
	ginkgo.Label("up-varsub-scope"),
	func() {
		var dtc *dockerTestContext

		ginkgo.BeforeEach(func(ctx context.Context) {
			var err error
			dtc = &dockerTestContext{}
			dtc.initialDir, err = os.Getwd()
			framework.ExpectNoError(err)

			dtc.f, err = setupDockerProvider(
				filepath.Join(dtc.initialDir, "bin"), "docker",
			)
			framework.ExpectNoError(err)
		})

		ginkgo.It(
			"containerEnv preserves container-scoped vars as literals",
			func(ctx context.Context) {
				tempDir, err := dtc.setupAndUp(ctx, "tests/up/testdata/docker-varsub-scope")
				framework.ExpectNoError(err)

				workspace, err := dtc.f.FindWorkspace(ctx, tempDir)
				framework.ExpectNoError(err)

				// containerEnv: ${containerWorkspaceFolder} should be preserved as literal
				// and resolved at runtime by the shell from the actual env var.
				cwf, err := dtc.execSSHCapture(ctx, workspace.ID, "cat $HOME/container-env-cwf.out")
				framework.ExpectNoError(err)
				gomega.Expect(cwf).To(gomega.ContainSubstring("/workspaces/"),
					"containerEnv containerWorkspaceFolder should resolve at runtime via shell")

				// containerEnv: ${containerWorkspaceFolderBasename} should be preserved as literal
				cwfb, err := dtc.execSSHCapture(
					ctx,
					workspace.ID,
					"cat $HOME/container-env-cwfb.out",
				)
				framework.ExpectNoError(err)
				gomega.Expect(cwfb).To(gomega.Equal(filepath.Base(tempDir)),
					"containerEnv containerWorkspaceFolderBasename should resolve at runtime via shell")

				// containerEnv: ${containerEnv:PATH} should remain literal
				cenv, err := dtc.execSSHCapture(
					ctx,
					workspace.ID,
					"cat $HOME/container-env-cenv.out",
				)
				framework.ExpectNoError(err)
				gomega.Expect(cenv).To(gomega.ContainSubstring("/usr/local/bin"),
					"containerEnv containerEnv:PATH should resolve at runtime via SubstituteContainerEnv")

				// containerEnv: ${localWorkspaceFolder} should resolve during substitution
				local, err := dtc.execSSHCapture(
					ctx,
					workspace.ID,
					"cat $HOME/container-env-local.out",
				)
				framework.ExpectNoError(err)
				gomega.Expect(framework.CleanString(local)).
					To(gomega.Equal(framework.CleanString(tempDir)),
						"containerEnv localWorkspaceFolder should resolve at substitution time")

				// remoteEnv: ${containerWorkspaceFolder} should fully resolve
				remoteCwf, err := dtc.execSSHCapture(
					ctx,
					workspace.ID,
					"cat $HOME/remote-env-cwf.out",
				)
				framework.ExpectNoError(err)
				gomega.Expect(framework.CleanString(remoteCwf)).
					To(gomega.ContainSubstring("/workspaces/"),
						"remoteEnv containerWorkspaceFolder should resolve at substitution time")

				// remoteEnv: ${containerWorkspaceFolderBasename} should fully resolve
				remoteCwfb, err := dtc.execSSHCapture(
					ctx,
					workspace.ID,
					"cat $HOME/remote-env-cwfb.out",
				)
				framework.ExpectNoError(err)
				gomega.Expect(remoteCwfb).To(gomega.Equal(filepath.Base(tempDir)),
					"remoteEnv containerWorkspaceFolderBasename should resolve at substitution time")

				// remoteEnv: ${containerEnv:PATH} resolved via SubstituteContainerEnv
				remoteCenv, err := dtc.execSSHCapture(
					ctx,
					workspace.ID,
					"cat $HOME/remote-env-cenv.out",
				)
				framework.ExpectNoError(err)
				gomega.Expect(remoteCenv).To(gomega.ContainSubstring("/usr/local/bin"),
					"remoteEnv containerEnv:PATH should resolve via SubstituteContainerEnv")

				// remoteEnv: ${localWorkspaceFolder} should fully resolve
				remoteLocal, err := dtc.execSSHCapture(
					ctx,
					workspace.ID,
					"cat $HOME/remote-env-local.out",
				)
				framework.ExpectNoError(err)
				gomega.Expect(framework.CleanString(remoteLocal)).
					To(gomega.Equal(framework.CleanString(tempDir)),
						"remoteEnv localWorkspaceFolder should resolve at substitution time")
			},
			ginkgo.SpecTimeout(framework.TimeoutShort()),
		)
	},
)
