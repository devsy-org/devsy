package extends

import (
	"context"
	"encoding/json"
	"os"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("extends property", ginkgo.Label("extends"), func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.It("resolves single-level extends inheriting parent fields", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")
		tempDir, err := framework.CopyToTempDirWithoutChdir(
			"tests/extends/testdata/single-level",
		)
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

		stdout, _, err := readConfiguration(ctx, f, tempDir)
		framework.ExpectNoError(err)

		config := parseConfigFromOutput(stdout)
		gomega.Expect(config).To(gomega.HaveKeyWithValue("name", "Single Level Child"))
		gomega.Expect(config).To(
			gomega.HaveKeyWithValue("image", "mcr.microsoft.com/devcontainers/base:ubuntu"),
		)
		gomega.Expect(config).To(gomega.HaveKeyWithValue("remoteUser", "vscode"))
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("deep merges map fields from parent and child", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")
		tempDir, err := framework.CopyToTempDirWithoutChdir(
			"tests/extends/testdata/deep-merge",
		)
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

		stdout, _, err := readConfiguration(ctx, f, tempDir)
		framework.ExpectNoError(err)

		config := parseConfigFromOutput(stdout)
		gomega.Expect(config).To(gomega.HaveKeyWithValue("name", "Deep Merge Child"))

		containerEnv, ok := config["containerEnv"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), "containerEnv should be an object")
		gomega.Expect(containerEnv).To(gomega.HaveKeyWithValue("SHARED_KEY", "from-child"))
		gomega.Expect(containerEnv).To(gomega.HaveKeyWithValue("BASE_ONLY", "base-value"))
		gomega.Expect(containerEnv).To(gomega.HaveKeyWithValue("CHILD_ONLY", "child-value"))

		features, ok := config["features"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), "features should be an object")
		gomega.Expect(features).To(gomega.HaveKey("ghcr.io/devcontainers/features/node:1"))
		gomega.Expect(features).To(gomega.HaveKey("ghcr.io/devcontainers/features/go:1"))
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("replaces array fields entirely from child", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")
		tempDir, err := framework.CopyToTempDirWithoutChdir(
			"tests/extends/testdata/array-replace",
		)
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

		stdout, _, err := readConfiguration(ctx, f, tempDir)
		framework.ExpectNoError(err)

		config := parseConfigFromOutput(stdout)
		gomega.Expect(config).To(gomega.HaveKeyWithValue("name", "Array Replace Child"))
		gomega.Expect(config).To(
			gomega.HaveKeyWithValue("image", "mcr.microsoft.com/devcontainers/base:ubuntu"),
		)

		forwardPorts, ok := config["forwardPorts"].([]any)
		gomega.Expect(ok).To(gomega.BeTrue(), "forwardPorts should be an array")
		gomega.Expect(forwardPorts).To(gomega.HaveLen(1))
		gomega.Expect(forwardPorts[0]).To(gomega.Equal("8080"))

		// capAdd from parent should remain since child didn't set it
		capAdd, ok := config["capAdd"].([]any)
		gomega.Expect(ok).To(gomega.BeTrue(), "capAdd should be inherited from parent")
		gomega.Expect(capAdd).To(gomega.ContainElement("SYS_PTRACE"))
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("resolves multi-level extends chain", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")
		tempDir, err := framework.CopyToTempDirWithoutChdir(
			"tests/extends/testdata/multi-level",
		)
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

		stdout, _, err := readConfiguration(ctx, f, tempDir)
		framework.ExpectNoError(err)

		config := parseConfigFromOutput(stdout)
		gomega.Expect(config).To(gomega.HaveKeyWithValue("name", "Multi Level Child"))
		gomega.Expect(config).To(
			gomega.HaveKeyWithValue("image", "mcr.microsoft.com/devcontainers/base:ubuntu"),
		)
		gomega.Expect(config).To(gomega.HaveKeyWithValue("remoteUser", "vscode"))

		containerEnv, ok := config["containerEnv"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), "containerEnv should be an object")
		gomega.Expect(containerEnv).To(gomega.HaveKeyWithValue("LEVEL", "child"))
		gomega.Expect(containerEnv).To(gomega.HaveKeyWithValue("PARENT_ONLY", "parent-value"))
		gomega.Expect(containerEnv).To(gomega.HaveKeyWithValue("GP_ONLY", "gp-value"))
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("returns error on circular extends", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")
		tempDir, err := framework.CopyToTempDirWithoutChdir(
			"tests/extends/testdata/cycle",
		)
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

		_, _, err = readConfiguration(ctx, f, tempDir)
		framework.ExpectError(err)
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("returns error when extends references missing file", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")
		tempDir, err := framework.CopyToTempDirWithoutChdir(
			"tests/extends/testdata/missing-file",
		)
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

		_, _, err = readConfiguration(ctx, f, tempDir)
		framework.ExpectError(err)
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))
})

func readConfiguration(
	ctx context.Context,
	f *framework.Framework,
	workspaceFolder string,
) (string, string, error) {
	return f.ExecCommandCapture(ctx, []string{
		"read-configuration",
		"--workspace-folder", workspaceFolder,
	})
}

func parseConfigFromOutput(stdout string) map[string]any {
	var result map[string]any
	err := json.Unmarshal([]byte(stdout), &result)
	framework.ExpectNoError(err, "output should be valid JSON")

	config, ok := result["configuration"].(map[string]any)
	gomega.Expect(ok).To(gomega.BeTrue(), "configuration should be an object")
	return config
}
