package templates

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	flagTemplateID      = "--template-id"
	flagWorkspaceFolder = "--workspace-folder"
	subCmdApply         = "apply"
)

var _ = ginkgo.Describe("templates command", ginkgo.Label("templates"), func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.Describe(subCmdApply, func() {
		ginkgo.It("applies a template from OCI registry", func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")
			tempDir, err := os.MkdirTemp("", "devsy-e2e-templates-apply-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

			_, _, err = f.ExecCommandCapture(ctx, []string{
				"templates", subCmdApply,
				flagTemplateID, "ghcr.io/devcontainers/templates/go:latest",
				flagWorkspaceFolder, tempDir,
			})
			framework.ExpectNoError(err)

			devcontainerPath := filepath.Join(tempDir, ".devcontainer", "devcontainer.json")
			_, err = os.Stat(devcontainerPath)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(),
				"devcontainer.json should exist after apply")

			data, err := os.ReadFile(devcontainerPath) //nolint:gosec // test path from MkdirTemp
			framework.ExpectNoError(err)

			var config map[string]any
			err = json.Unmarshal(data, &config)
			framework.ExpectNoError(err, "devcontainer.json should be valid JSON")
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

		ginkgo.It("applies template with features", func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")
			tempDir, err := os.MkdirTemp("", "devsy-e2e-templates-features-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

			_, _, err = f.ExecCommandCapture(ctx, []string{
				"templates", subCmdApply,
				flagTemplateID, "ghcr.io/devcontainers/templates/go:latest",
				flagWorkspaceFolder, tempDir,
				"--features", "ghcr.io/devcontainers/features/node:1",
			})
			framework.ExpectNoError(err)

			devcontainerPath := filepath.Join(tempDir, ".devcontainer", "devcontainer.json")
			data, err := os.ReadFile(devcontainerPath) //nolint:gosec // test path from MkdirTemp
			framework.ExpectNoError(err)

			var config map[string]any
			err = json.Unmarshal(data, &config)
			framework.ExpectNoError(err)

			features, ok := config["features"].(map[string]any)
			gomega.Expect(ok).To(gomega.BeTrue(), "features should be present")
			gomega.Expect(features).To(
				gomega.HaveKey("ghcr.io/devcontainers/features/node:1"),
			)
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

		ginkgo.It("fails with invalid template-id", func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")
			tempDir, err := os.MkdirTemp("", "devsy-e2e-templates-invalid-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

			_, _, err = f.ExecCommandCapture(ctx, []string{
				"templates", subCmdApply,
				flagTemplateID, "ghcr.io/nonexistent/template-that-does-not-exist:v999",
				flagWorkspaceFolder, tempDir,
			})
			framework.ExpectError(err)
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))
	})

	ginkgo.Describe("metadata", func() {
		ginkgo.It("fetches template metadata from OCI registry", func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"templates", "metadata",
				flagTemplateID, "ghcr.io/devcontainers/templates/go:latest",
			})
			framework.ExpectNoError(err)

			var metadata map[string]any
			err = json.Unmarshal([]byte(stdout), &metadata)
			framework.ExpectNoError(err, "metadata output should be valid JSON")

			gomega.Expect(metadata).To(gomega.HaveKey("id"))
			gomega.Expect(metadata["id"]).To(gomega.Equal("go"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

		ginkgo.It("fails with invalid template-id", func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			_, _, err := f.ExecCommandCapture(ctx, []string{
				"templates", "metadata",
				flagTemplateID, "ghcr.io/nonexistent/template-that-does-not-exist:v999",
			})
			framework.ExpectError(err)
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))
	})
})
