package ide

import (
	"context"
	"os"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/config"
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

	// This asserts on the host-side resolved workspace config rather than a
	// container filesystem path: ws.IDE.Name is exactly the value
	// applySkipLaunchIDEDefault (up_validate.go) is responsible for defaulting
	// to "none", and it is also exactly the value installIDE
	// (cmd/internal/agentcontainer/setup.go) gates the container-side IDE
	// server download on. Asserting on a container path like
	// ~/.openvscode-server is brittle to remoteUser/image changes (it varies
	// per devcontainer) and would not actually discriminate a reverted fix
	// here, since --ide=openvscode is never requested either way.
	ginkgo.It("defaults the resolved IDE to none when --ide is omitted",
		func(ctx context.Context) {
			f, tempDir := setupBrowserIDE(ctx, initialDir)

			err := f.DevsyUpWithIDE(ctx, "--ide-launch=skip", tempDir)
			framework.ExpectNoError(err)

			ws, err := f.FindWorkspace(ctx, tempDir)
			framework.ExpectNoError(err)
			gomega.Expect(ws).NotTo(gomega.BeNil())
			gomega.Expect(ws.IDE.Name).To(gomega.Equal(string(config.IDENone)),
				"--ide-launch=skip without an explicit --ide must resolve IDE to "+
					"none, so installIDE never downloads an IDE server binary "+
					"container-side")
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))
})
