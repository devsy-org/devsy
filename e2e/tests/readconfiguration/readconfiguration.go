package readconfiguration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("read-configuration command", ginkgo.Label("read-configuration"), func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.It("outputs valid JSON with expected fields", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")
		tempDir, err := framework.CopyToTempDirWithoutChdir(
			"tests/readconfiguration/testdata",
		)
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

		stdout, _, err := f.ExecCommandCapture(ctx, []string{
			"read-configuration",
			"--workspace-folder", tempDir,
		})
		framework.ExpectNoError(err)

		var result map[string]any
		err = json.Unmarshal([]byte(stdout), &result)
		framework.ExpectNoError(err, "output should be valid JSON")

		gomega.Expect(result).To(gomega.HaveKey("configuration"))
		gomega.Expect(result).To(gomega.HaveKey("workspace"))

		config, ok := result["configuration"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), "configuration should be an object")
		gomega.Expect(config).To(gomega.HaveKeyWithValue("name", "Test Read Configuration"))
		gomega.Expect(config).To(
			gomega.HaveKeyWithValue("image", "mcr.microsoft.com/devcontainers/base:ubuntu"),
		)
		gomega.Expect(config).To(gomega.HaveKey("features"))
		gomega.Expect(config).To(
			gomega.HaveKeyWithValue("remoteUser", "vscode"),
		)

		ws, ok := result["workspace"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), "workspace should be an object")
		gomega.Expect(ws).To(gomega.HaveKey("workspaceFolder"))

		gomega.Expect(result).NotTo(gomega.HaveKey("features"),
			"features should not appear without --include-features-configuration")
		gomega.Expect(result).NotTo(gomega.HaveKey("mergedConfiguration"),
			"merged config should not appear without --include-merged-configuration")
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("includes features with --include-features-configuration", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")
		tempDir, err := framework.CopyToTempDirWithoutChdir(
			"tests/readconfiguration/testdata",
		)
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

		stdout, _, err := f.ExecCommandCapture(ctx, []string{
			"read-configuration",
			"--workspace-folder", tempDir,
			"--include-features-configuration",
		})
		framework.ExpectNoError(err)

		var result map[string]any
		err = json.Unmarshal([]byte(stdout), &result)
		framework.ExpectNoError(err)

		gomega.Expect(result).To(gomega.HaveKey("features"))
		features, ok := result["features"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), "features should be an object")
		gomega.Expect(features).To(
			gomega.HaveKey("ghcr.io/devcontainers/features/node:1"),
		)
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("includes merged configuration with --include-merged-configuration",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")
			tempDir, err := framework.CopyToTempDirWithoutChdir(
				"tests/readconfiguration/testdata",
			)
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"read-configuration",
				"--workspace-folder", tempDir,
				"--include-merged-configuration",
			})
			framework.ExpectNoError(err)

			var result map[string]any
			err = json.Unmarshal([]byte(stdout), &result)
			framework.ExpectNoError(err)

			gomega.Expect(result).To(gomega.HaveKey("mergedConfiguration"))
			merged, ok := result["mergedConfiguration"].(map[string]any)
			gomega.Expect(ok).To(gomega.BeTrue(), "mergedConfiguration should be an object")
			gomega.Expect(merged).To(
				gomega.HaveKeyWithValue("remoteUser", "vscode"),
			)
			gomega.Expect(merged).To(
				gomega.HaveKeyWithValue("image", "mcr.microsoft.com/devcontainers/base:ubuntu"),
			)
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("fails with missing workspace folder", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")

		_, _, err := f.ExecCommandCapture(ctx, []string{
			"read-configuration",
			"--workspace-folder", "/nonexistent/path/that/does/not/exist",
		})
		framework.ExpectError(err)
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("fails without --workspace-folder flag", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")

		_, _, err := f.ExecCommandCapture(ctx, []string{
			"read-configuration",
		})
		framework.ExpectError(err)
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("expands forwardPorts range syntax in merged configuration",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")
			tempDir, err := framework.CopyToTempDirWithoutChdir(
				"tests/readconfiguration/testdata-port-range",
			)
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"read-configuration",
				"--workspace-folder", tempDir,
				"--include-merged-configuration",
			})
			framework.ExpectNoError(err)

			var result map[string]any
			err = json.Unmarshal([]byte(stdout), &result)
			framework.ExpectNoError(err)

			merged, ok := result["mergedConfiguration"].(map[string]any)
			gomega.Expect(ok).To(gomega.BeTrue())

			portsRaw, ok := merged["forwardPorts"].([]any)
			gomega.Expect(ok).To(
				gomega.BeTrue(),
				"forwardPorts should be an array",
			)

			var ports []string
			for _, p := range portsRaw {
				s, ok := p.(string)
				gomega.Expect(ok).To(gomega.BeTrue())
				ports = append(ports, s)
			}

			gomega.Expect(ports).To(gomega.ContainElement("8080"))
			gomega.Expect(ports).To(gomega.ContainElement("3000"))
			gomega.Expect(ports).To(gomega.ContainElement("3005"))
			gomega.Expect(ports).To(gomega.HaveLen(7))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("preserves otherPortsAttributes in merged configuration",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")
			tempDir, err := framework.CopyToTempDirWithoutChdir(
				"tests/readconfiguration/testdata-port-attributes",
			)
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"read-configuration",
				"--workspace-folder", tempDir,
				"--include-merged-configuration",
			})
			framework.ExpectNoError(err)

			var result map[string]any
			err = json.Unmarshal([]byte(stdout), &result)
			framework.ExpectNoError(err)

			merged, ok := result["mergedConfiguration"].(map[string]any)
			gomega.Expect(ok).To(gomega.BeTrue())

			pa, ok := merged["portsAttributes"].(map[string]any)
			gomega.Expect(ok).To(gomega.BeTrue(), "portsAttributes should be present")
			entry, ok := pa["8080"].(map[string]any)
			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(entry["label"]).To(gomega.Equal("web"))
			gomega.Expect(entry["onAutoForward"]).To(gomega.Equal("silent"))

			opa, ok := merged["otherPortsAttributes"].(map[string]any)
			gomega.Expect(ok).To(
				gomega.BeTrue(),
				"otherPortsAttributes should be present in merged config",
			)
			gomega.Expect(opa["onAutoForward"]).To(gomega.Equal("ignore"))
			gomega.Expect(opa["label"]).To(gomega.Equal("default"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	// Size-limit warning tested via unit test in metadata_test.go — impractical to generate >100KB metadata in E2E.

	ginkgo.It("preserves containerEnv in configuration output", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")
		tempDir, err := framework.CopyToTempDirWithoutChdir(
			"tests/readconfiguration/testdata-container-env",
		)
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

		stdout, _, err := f.ExecCommandCapture(ctx, []string{
			"read-configuration",
			"--workspace-folder", tempDir,
		})
		framework.ExpectNoError(err)

		var result map[string]any
		err = json.Unmarshal([]byte(stdout), &result)
		framework.ExpectNoError(err, "output should be valid JSON")

		config, ok := result["configuration"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), "configuration should be an object")

		cenv, ok := config["containerEnv"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), "containerEnv should be an object")
		gomega.Expect(cenv).To(gomega.HaveKeyWithValue("TEST_META_VAR", "meta-test-value"))
		gomega.Expect(cenv).To(gomega.HaveKeyWithValue("ANOTHER_VAR", "another-value"))
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("preserves containerEnv in merged configuration", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")
		tempDir, err := framework.CopyToTempDirWithoutChdir(
			"tests/readconfiguration/testdata-container-env",
		)
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

		stdout, _, err := f.ExecCommandCapture(ctx, []string{
			"read-configuration",
			"--workspace-folder", tempDir,
			"--include-merged-configuration",
		})
		framework.ExpectNoError(err)

		var result map[string]any
		err = json.Unmarshal([]byte(stdout), &result)
		framework.ExpectNoError(err)

		merged, ok := result["mergedConfiguration"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), "mergedConfiguration should be an object")

		cenv, ok := merged["containerEnv"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), "containerEnv should be present in merged config")
		gomega.Expect(cenv).To(gomega.HaveKeyWithValue("TEST_META_VAR", "meta-test-value"))
		gomega.Expect(cenv).To(gomega.HaveKeyWithValue("ANOTHER_VAR", "another-value"))
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("preserves hostRequirements in configuration output", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")
		tempDir, err := framework.CopyToTempDirWithoutChdir(
			"tests/readconfiguration/testdata-host-requirements",
		)
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

		stdout, _, err := f.ExecCommandCapture(ctx, []string{
			"read-configuration",
			"--workspace-folder", tempDir,
		})
		framework.ExpectNoError(err)

		var result map[string]any
		err = json.Unmarshal([]byte(stdout), &result)
		framework.ExpectNoError(err, "output should be valid JSON")

		config, ok := result["configuration"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), "configuration should be an object")

		hr, ok := config["hostRequirements"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), "hostRequirements should be an object")
		gomega.Expect(hr["cpus"]).To(gomega.BeEquivalentTo(4))
		gomega.Expect(hr["memory"]).To(gomega.Equal("8gb"))
		gomega.Expect(hr["storage"]).To(gomega.Equal("32gb"))
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("reads configuration from a running container via --container-id",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			metadataLabel := `[{"remoteUser":"testuser","customizations":{"vscode":{"extensions":["ms-python.python"]}}}]`
			out, err := exec.CommandContext(ctx, "docker", "run", "-d",
				"--label", "devcontainer.metadata="+metadataLabel,
				"mcr.microsoft.com/devcontainers/base:ubuntu",
				"sleep", "infinity",
			).Output()
			framework.ExpectNoError(err)
			containerID := strings.TrimSpace(string(out))
			ginkgo.DeferCleanup(func() {
				_ = exec.Command("docker", "rm", "-f", containerID).Run() //nolint:gosec // G204
			})

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"read-configuration",
				"--container-id", containerID,
			})
			framework.ExpectNoError(err)

			var result map[string]any
			err = json.Unmarshal([]byte(stdout), &result)
			framework.ExpectNoError(err, "output should be valid JSON")

			gomega.Expect(result).To(gomega.HaveKey("configuration"))
			gomega.Expect(result).To(gomega.HaveKey("workspace"))

			config, ok := result["configuration"].(map[string]any)
			gomega.Expect(ok).To(gomega.BeTrue(), "configuration should be an object")
			gomega.Expect(config).To(gomega.HaveKeyWithValue("remoteUser", "testuser"))

			ws, ok := result["workspace"].(map[string]any)
			gomega.Expect(ok).To(gomega.BeTrue(), "workspace should be an object")
			gomega.Expect(ws).To(gomega.HaveKey("workspaceFolder"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))
})
