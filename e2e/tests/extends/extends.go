package extends

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
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
			gomega.HaveKeyWithValue("image", "ghcr.io/devsy-org/test-images/base:ubuntu"),
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
			gomega.HaveKeyWithValue("image", "ghcr.io/devsy-org/test-images/base:ubuntu"),
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
			gomega.HaveKeyWithValue("image", "ghcr.io/devsy-org/test-images/base:ubuntu"),
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

	ginkgo.It("resolves array extends with multiple parents", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")
		tempDir, err := framework.CopyToTempDirWithoutChdir(
			"tests/extends/testdata/array-extends",
		)
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

		stdout, _, err := readConfiguration(ctx, f, tempDir)
		framework.ExpectNoError(err)

		config := parseConfigFromOutput(stdout)
		gomega.Expect(config).To(gomega.HaveKeyWithValue("name", "Array Extends Child"))
		gomega.Expect(config).To(
			gomega.HaveKeyWithValue("image", "ghcr.io/devsy-org/test-images/base:ubuntu"),
		)
		gomega.Expect(config).To(gomega.HaveKeyWithValue("remoteUser", "vscode"))

		containerEnv, ok := config["containerEnv"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), "containerEnv should be an object")
		gomega.Expect(containerEnv).To(gomega.HaveKeyWithValue("FROM_BASE", "base-value"))
		gomega.Expect(containerEnv).To(
			gomega.HaveKeyWithValue("FROM_MIDDLEWARE", "middleware-value"),
		)
		gomega.Expect(containerEnv).To(gomega.HaveKeyWithValue("FROM_CHILD", "child-value"))
		gomega.Expect(containerEnv).To(gomega.HaveKeyWithValue("SHARED", "from-middleware"))
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

	ginkgo.It("resolves OCI registry extends reference", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")

		srv := httptest.NewServer(registry.New())
		ginkgo.DeferCleanup(func() { srv.Close() })

		regHost := strings.TrimPrefix(srv.URL, "http://")

		parentJSON := `{
			"name": "oci-parent",
			"image": "ubuntu:22.04",
			"remoteUser": "vscode",
			"containerEnv": {"FROM_OCI": "oci-value"}
		}`
		pushOCIImage(regHost+"/test/devcontainer-base:latest", parentJSON)

		tempDir, err := os.MkdirTemp("", "oci-extends-*")
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

		devcontainerDir := filepath.Join(tempDir, ".devcontainer")
		framework.ExpectNoError(os.MkdirAll(devcontainerDir, 0o750))

		childJSON := fmt.Sprintf(`{
			"name": "OCI Child",
			"extends": "%s/test/devcontainer-base:latest"
		}`, regHost)
		framework.ExpectNoError(
			os.WriteFile(
				filepath.Join(devcontainerDir, "devcontainer.json"),
				[]byte(childJSON),
				0o600,
			),
		)

		stdout, _, err := readConfiguration(ctx, f, tempDir)
		framework.ExpectNoError(err)

		config := parseConfigFromOutput(stdout)
		gomega.Expect(config).To(gomega.HaveKeyWithValue("name", "OCI Child"))
		gomega.Expect(config).To(gomega.HaveKeyWithValue("image", "ubuntu:22.04"))
		gomega.Expect(config).To(gomega.HaveKeyWithValue("remoteUser", "vscode"))

		containerEnv, ok := config["containerEnv"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), "containerEnv should be an object")
		gomega.Expect(containerEnv).To(gomega.HaveKeyWithValue("FROM_OCI", "oci-value"))
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It(
		"resolves OCI extends with deep merge of parent fields",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			srv := httptest.NewServer(registry.New())
			ginkgo.DeferCleanup(func() { srv.Close() })

			regHost := strings.TrimPrefix(srv.URL, "http://")

			parentJSON := `{
			"image": "ghcr.io/devsy-org/test-images/base:ubuntu",
			"containerEnv": {"PARENT_KEY": "parent-value", "SHARED": "from-parent"},
			"features": {"ghcr.io/devcontainers/features/node:1": {}}
		}`
			pushOCIImage(regHost+"/test/merge-base:v1", parentJSON)

			tempDir, err := os.MkdirTemp("", "oci-extends-merge-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

			devcontainerDir := filepath.Join(tempDir, ".devcontainer")
			framework.ExpectNoError(os.MkdirAll(devcontainerDir, 0o750))

			childJSON := fmt.Sprintf(`{
			"name": "OCI Merge Child",
			"extends": "%s/test/merge-base:v1",
			"containerEnv": {"CHILD_KEY": "child-value", "SHARED": "from-child"},
			"features": {"ghcr.io/devcontainers/features/go:1": {}}
		}`, regHost)
			framework.ExpectNoError(
				os.WriteFile(
					filepath.Join(devcontainerDir, "devcontainer.json"),
					[]byte(childJSON),
					0o600,
				),
			)

			stdout, _, err := readConfiguration(ctx, f, tempDir)
			framework.ExpectNoError(err)

			config := parseConfigFromOutput(stdout)
			gomega.Expect(config).To(gomega.HaveKeyWithValue("name", "OCI Merge Child"))
			gomega.Expect(config).To(
				gomega.HaveKeyWithValue(
					"image",
					"ghcr.io/devsy-org/test-images/base:ubuntu",
				),
			)

			containerEnv, ok := config["containerEnv"].(map[string]any)
			gomega.Expect(ok).To(gomega.BeTrue(), "containerEnv should be an object")
			gomega.Expect(containerEnv).To(
				gomega.HaveKeyWithValue("PARENT_KEY", "parent-value"),
			)
			gomega.Expect(containerEnv).To(
				gomega.HaveKeyWithValue("CHILD_KEY", "child-value"),
			)
			gomega.Expect(containerEnv).To(gomega.HaveKeyWithValue("SHARED", "from-child"))

			features, ok := config["features"].(map[string]any)
			gomega.Expect(ok).To(gomega.BeTrue(), "features should be an object")
			gomega.Expect(features).To(
				gomega.HaveKey("ghcr.io/devcontainers/features/node:1"),
			)
			gomega.Expect(features).To(
				gomega.HaveKey("ghcr.io/devcontainers/features/go:1"),
			)
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))
})

func pushOCIImage(refStr, jsonContent string) {
	layer := static.NewLayer(buildOCITarGz("devcontainer.json", jsonContent), types.OCILayer)

	img, err := mutate.AppendLayers(empty.Image, layer)
	framework.ExpectNoError(err)

	ref, err := name.ParseReference(refStr, name.Insecure)
	framework.ExpectNoError(err)

	framework.ExpectNoError(remote.Write(ref, img))
}

func buildOCITarGz(filename, content string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	hdr := &tar.Header{
		Name: filename,
		Mode: 0o644,
		Size: int64(len(content)),
	}
	gomega.Expect(tw.WriteHeader(hdr)).To(gomega.Succeed())
	_, err := tw.Write([]byte(content))
	framework.ExpectNoError(err)
	gomega.Expect(tw.Close()).To(gomega.Succeed())
	gomega.Expect(gz.Close()).To(gomega.Succeed())
	return buf.Bytes()
}

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
