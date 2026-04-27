package exec

import (
	"context"
	"os"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("devsy exec test suite", ginkgo.Label("exec"), ginkgo.Ordered, func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.It("should exec a command in a running workspace container",
		func(ctx context.Context) {
			tempDir, err := framework.CopyToTempDir("tests/exec/testdata")
			framework.ExpectNoError(err)

			f, err := framework.SetupDockerProvider(initialDir+"/bin", "docker")
			framework.ExpectNoError(err)

			ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
				_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
				framework.CleanupTempDir(initialDir, tempDir)
			})

			err = f.DevsyUp(ctx, tempDir)
			framework.ExpectNoError(err)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"exec",
				"--workspace-folder", tempDir,
				"--", "echo", "-n", "hello",
			})
			framework.ExpectNoError(err)
			gomega.Expect(stdout).To(gomega.Equal("hello"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should pass remote-env to the container",
		func(ctx context.Context) {
			tempDir, err := framework.CopyToTempDir("tests/exec/testdata")
			framework.ExpectNoError(err)

			f, err := framework.SetupDockerProvider(initialDir+"/bin", "docker")
			framework.ExpectNoError(err)

			ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
				_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
				framework.CleanupTempDir(initialDir, tempDir)
			})

			err = f.DevsyUp(ctx, tempDir)
			framework.ExpectNoError(err)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"exec",
				"--workspace-folder", tempDir,
				"--remote-env", "MY_TEST_VAR=test_value",
				"--", "sh", "-c", "echo -n $MY_TEST_VAR",
			})
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(stdout)).To(gomega.Equal("test_value"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should fail without --workspace-folder flag",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			_, _, err := f.ExecCommandCapture(ctx, []string{
				"exec",
				"--", "echo", "hello",
			})
			framework.ExpectError(err)
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))
})
