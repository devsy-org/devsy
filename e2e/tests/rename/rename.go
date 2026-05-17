package rename

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const statusError = "error"

func addDockerProvider(ctx context.Context, f *framework.Framework, name string) error {
	dockerHost := os.Getenv("DOCKER_HOST")
	if dockerHost != "" && strings.Contains(dockerHost, "podman") {
		return f.DevsyProviderAdd(ctx, "docker", "--name", name, "--option=DOCKER_PATH=podman")
	}
	return f.DevsyProviderAdd(ctx, "docker", "--name", name)
}

var _ = ginkgo.Describe(
	"devsy rename test suite",
	ginkgo.Label("rename"),
	ginkgo.Ordered,
	func() {
		var initialDir string

		ginkgo.BeforeAll(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It("should rename a stopped workspace to a new, valid name",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")

				providerName := "rename-provider1"
				workspaceName := "rename-ws1"
				renamedWorkspaceName := "renamed-ws1"

				err := f.DevsyProviderDelete(ctx, providerName, "--ignore-not-found")
				framework.ExpectNoError(err)

				tempDir, err := framework.CopyToTempDir("tests/up/testdata/no-devcontainer")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				err = addDockerProvider(ctx, f, providerName)
				framework.ExpectNoError(err)
				err = f.DevsyProviderUse(ctx, providerName)
				framework.ExpectNoError(err)

				err = f.DevsyUp(ctx, tempDir, "--id", workspaceName)
				framework.ExpectNoError(err)

				gomega.Eventually(func() string {
					status, err := f.DevsyStatus(ctx, workspaceName)
					if err != nil {
						return statusError
					}
					return string(status.State)
				}).WithTimeout(30 * time.Second).
					WithPolling(1 * time.Second).
					Should(gomega.Equal("Running"))

				err = f.DevsyStop(ctx, workspaceName)
				framework.ExpectNoError(err)

				err = f.DevsyRename(ctx, workspaceName, renamedWorkspaceName)
				framework.ExpectNoError(err)

				// Old name should not be found
				_, err = f.FindWorkspace(ctx, workspaceName)
				gomega.Expect(err).To(gomega.HaveOccurred())

				// New name should exist
				ws, err := f.FindWorkspace(ctx, renamedWorkspaceName)
				framework.ExpectNoError(err)
				gomega.Expect(ws.ID).To(gomega.Equal(renamedWorkspaceName))

				// Can start the renamed workspace
				err = f.DevsyUp(ctx, renamedWorkspaceName)
				framework.ExpectNoError(err)

				gomega.Eventually(func() string {
					status, err := f.DevsyStatus(ctx, renamedWorkspaceName)
					if err != nil {
						return statusError
					}
					return string(status.State)
				}).WithTimeout(30 * time.Second).
					WithPolling(1 * time.Second).
					Should(gomega.Equal("Running"))

				err = f.DevsyStop(ctx, renamedWorkspaceName)
				framework.ExpectNoError(err)
				err = f.DevsyWorkspaceDelete(ctx, renamedWorkspaceName)
				framework.ExpectNoError(err)

				gomega.Eventually(func() error {
					_, err := f.FindWorkspace(ctx, renamedWorkspaceName)
					return err
				}).WithTimeout(30 * time.Second).
					WithPolling(1 * time.Second).
					Should(gomega.HaveOccurred())

				err = f.DevsyProviderDelete(ctx, providerName)
				framework.ExpectNoError(err)
			})

		ginkgo.It("should auto-stop a running workspace and rename it",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")

				providerName := "rename-provider2"
				workspaceName := "rename-ws2"
				renamedWorkspaceName := "renamed-ws2"

				err := f.DevsyProviderDelete(ctx, providerName, "--ignore-not-found")
				framework.ExpectNoError(err)

				tempDir, err := framework.CopyToTempDir("tests/up/testdata/no-devcontainer")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				err = addDockerProvider(ctx, f, providerName)
				framework.ExpectNoError(err)
				err = f.DevsyProviderUse(ctx, providerName)
				framework.ExpectNoError(err)

				err = f.DevsyUp(ctx, tempDir, "--id", workspaceName)
				framework.ExpectNoError(err)

				gomega.Eventually(func() string {
					status, err := f.DevsyStatus(ctx, workspaceName)
					if err != nil {
						return statusError
					}
					return string(status.State)
				}).WithTimeout(30 * time.Second).
					WithPolling(1 * time.Second).
					Should(gomega.Equal("Running"))

				// Rename while running — should auto-stop
				err = f.DevsyRename(ctx, workspaceName, renamedWorkspaceName)
				framework.ExpectNoError(err)

				_, err = f.FindWorkspace(ctx, workspaceName)
				gomega.Expect(err).To(gomega.HaveOccurred())

				ws, err := f.FindWorkspace(ctx, renamedWorkspaceName)
				framework.ExpectNoError(err)
				gomega.Expect(ws.ID).To(gomega.Equal(renamedWorkspaceName))

				err = f.DevsyWorkspaceDelete(ctx, renamedWorkspaceName)
				framework.ExpectNoError(err)

				gomega.Eventually(func() error {
					_, err := f.FindWorkspace(ctx, renamedWorkspaceName)
					return err
				}).WithTimeout(30 * time.Second).
					WithPolling(1 * time.Second).
					Should(gomega.HaveOccurred())

				err = f.DevsyProviderDelete(ctx, providerName)
				framework.ExpectNoError(err)
			})

		ginkgo.It("should fail to rename a workspace to a name that already exists",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")

				providerName := "rename-provider3"
				workspaceA := "rename-ws3a"
				workspaceB := "rename-ws3b"

				err := f.DevsyProviderDelete(ctx, providerName, "--ignore-not-found")
				framework.ExpectNoError(err)

				tempDir, err := framework.CopyToTempDir("tests/up/testdata/no-devcontainer")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				err = addDockerProvider(ctx, f, providerName)
				framework.ExpectNoError(err)
				err = f.DevsyProviderUse(ctx, providerName)
				framework.ExpectNoError(err)

				err = f.DevsyUp(ctx, tempDir, "--id", workspaceA)
				framework.ExpectNoError(err)
				err = f.DevsyStop(ctx, workspaceA)
				framework.ExpectNoError(err)

				err = f.DevsyUp(ctx, tempDir, "--id", workspaceB)
				framework.ExpectNoError(err)
				err = f.DevsyStop(ctx, workspaceB)
				framework.ExpectNoError(err)

				// Attempt rename to existing name
				err = f.DevsyRename(ctx, workspaceA, workspaceB)
				framework.ExpectError(err)

				// Both should still exist
				_, err = f.FindWorkspace(ctx, workspaceA)
				framework.ExpectNoError(err)
				_, err = f.FindWorkspace(ctx, workspaceB)
				framework.ExpectNoError(err)

				err = f.DevsyWorkspaceDelete(ctx, workspaceA)
				framework.ExpectNoError(err)
				err = f.DevsyWorkspaceDelete(ctx, workspaceB)
				framework.ExpectNoError(err)

				err = f.DevsyProviderDelete(ctx, providerName)
				framework.ExpectNoError(err)
			})

		ginkgo.It("should fail to rename a non-existent workspace",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")

				err := f.DevsyRename(ctx, "non-existent-ws4", "new-name4")
				framework.ExpectError(err)
			})

		ginkgo.It("should fail to rename a workspace to an invalid name",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")

				providerName := "rename-provider5"
				workspaceName := "rename-ws5"

				err := f.DevsyProviderDelete(ctx, providerName, "--ignore-not-found")
				framework.ExpectNoError(err)

				tempDir, err := framework.CopyToTempDir("tests/up/testdata/no-devcontainer")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				err = addDockerProvider(ctx, f, providerName)
				framework.ExpectNoError(err)
				err = f.DevsyProviderUse(ctx, providerName)
				framework.ExpectNoError(err)

				err = f.DevsyUp(ctx, tempDir, "--id", workspaceName)
				framework.ExpectNoError(err)
				err = f.DevsyStop(ctx, workspaceName)
				framework.ExpectNoError(err)

				// Invalid characters
				err = f.DevsyRename(ctx, workspaceName, "invalid/name5")
				framework.ExpectError(err)

				// Workspace should still exist with original name
				_, err = f.FindWorkspace(ctx, workspaceName)
				framework.ExpectNoError(err)

				// Same name
				err = f.DevsyRename(ctx, workspaceName, workspaceName)
				framework.ExpectError(err)

				err = f.DevsyWorkspaceDelete(ctx, workspaceName)
				framework.ExpectNoError(err)

				err = f.DevsyProviderDelete(ctx, providerName)
				framework.ExpectNoError(err)
			})

		// Verify that the workspace ID is properly updated in the config after rename.
		ginkgo.It("should update workspace ID in config after rename",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")

				providerName := "rename-provider6"
				workspaceName := "rename-ws6"
				renamedWorkspaceName := "renamed-ws6"

				err := f.DevsyProviderDelete(ctx, providerName, "--ignore-not-found")
				framework.ExpectNoError(err)

				tempDir, err := framework.CopyToTempDir("tests/up/testdata/no-devcontainer")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				err = addDockerProvider(ctx, f, providerName)
				framework.ExpectNoError(err)
				err = f.DevsyProviderUse(ctx, providerName)
				framework.ExpectNoError(err)

				err = f.DevsyUp(ctx, tempDir, "--id", workspaceName)
				framework.ExpectNoError(err)
				err = f.DevsyStop(ctx, workspaceName)
				framework.ExpectNoError(err)

				// Get workspace UID before rename
				wsBefore, err := f.FindWorkspace(ctx, workspaceName)
				framework.ExpectNoError(err)
				uidBefore := wsBefore.UID

				err = f.DevsyRename(ctx, workspaceName, renamedWorkspaceName)
				framework.ExpectNoError(err)

				wsAfter, err := f.FindWorkspace(ctx, renamedWorkspaceName)
				framework.ExpectNoError(err)

				// ID should be updated
				gomega.Expect(wsAfter.ID).To(gomega.Equal(renamedWorkspaceName))
				// UID should be preserved
				gomega.Expect(wsAfter.UID).To(gomega.Equal(uidBefore))
				// Provider should be preserved
				gomega.Expect(wsAfter.Provider.Name).To(gomega.Equal(providerName))

				err = f.DevsyWorkspaceDelete(ctx, renamedWorkspaceName)
				framework.ExpectNoError(err)

				gomega.Eventually(func() error {
					_, err := f.FindWorkspace(ctx, renamedWorkspaceName)
					return err
				}).WithTimeout(30 * time.Second).
					WithPolling(1 * time.Second).
					Should(gomega.HaveOccurred())

				err = f.DevsyProviderDelete(ctx, providerName)
				framework.ExpectNoError(err)
			})
	})
