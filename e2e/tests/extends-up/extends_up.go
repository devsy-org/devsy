package extendsup

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("extends up", ginkgo.Label("extends-up"), func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.It("starts container with merged config from extends chain", func(ctx context.Context) {
		f, err := framework.SetupDockerProvider(filepath.Join(initialDir, "bin"), "docker")
		framework.ExpectNoError(err)

		tempDir, err := framework.CopyToTempDir("tests/extends-up/testdata/up-extends")
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
		ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

		err = f.DevsyUp(ctx, tempDir)
		framework.ExpectNoError(err)

		// Verify env from parent (base.json)
		out, err := f.DevsySSH(ctx, tempDir, "echo -n $FROM_BASE")
		framework.ExpectNoError(err)
		gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("base-value"))

		// Verify env from child (devcontainer.json)
		out, err = f.DevsySSH(ctx, tempDir, "echo -n $FROM_CHILD")
		framework.ExpectNoError(err)
		gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("child-value"))
	}, ginkgo.SpecTimeout(framework.TimeoutLong()))
})
