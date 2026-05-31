package provider

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

func addDockerProvider(ctx context.Context, f *framework.Framework, name string) error {
	dockerHost := os.Getenv("DOCKER_HOST")
	if dockerHost != "" && strings.Contains(dockerHost, "podman") {
		return f.DevsyProviderAdd(ctx, "docker", "--name", name, "--option=DOCKER_PATH=podman")
	}
	return f.DevsyProviderAdd(ctx, "docker", "--name", name)
}

var _ = ginkgo.Describe(
	"devsy provider test suite",
	ginkgo.Label("provider"),
	ginkgo.Ordered,
	func() {
		var initialDir string

		ginkgo.BeforeAll(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It("should add simple provider and delete it",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				tempDir, err := framework.CopyToTempDir(
					"tests/provider/testdata/simple-k8s-provider",
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				f := framework.NewDefaultFramework(initialDir + "/bin")

				err = f.DevsyProviderDelete(ctx, "provider1", "--ignore-not-found")
				framework.ExpectNoError(err)

				err = f.DevsyProviderAdd(ctx, tempDir+"/provider1.yaml")
				framework.ExpectNoError(err)

				err = f.DevsyProviderUse(ctx, "provider1")
				framework.ExpectNoError(err)
				err = f.DevsyProviderUse(ctx, "providerX")
				framework.ExpectError(err)

				err = f.DevsyProviderDelete(ctx, "provider1")
				framework.ExpectNoError(err)

				err = f.DevsyProviderUse(ctx, "provider1")
				framework.ExpectError(err)
			})

		ginkgo.It("should add simple provider and update it",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				tempDir, err := framework.CopyToTempDir(
					"tests/provider/testdata/simple-k8s-provider",
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				f := framework.NewDefaultFramework(initialDir + "/bin")

				err = f.DevsyProviderDelete(ctx, "provider2", "--ignore-not-found")
				framework.ExpectNoError(err)

				err = f.DevsyProviderAdd(ctx, tempDir+"/provider2.yaml")
				framework.ExpectNoError(err)
				err = f.DevsyProviderUse(ctx, "provider2")
				framework.ExpectNoError(err)

				checkCtx, cancel := context.WithDeadline(
					ctx,
					time.Now().Add(30*time.Second),
				)
				err = f.DevsyProviderOptionsCheckNamespaceDescription(
					checkCtx,
					"provider2",
					"The namespace to use",
				)
				framework.ExpectNoError(err)
				cancel()

				err = f.DevsyProviderSetSource(
					ctx,
					"provider2",
					tempDir+"/provider2-update.yaml",
				)
				framework.ExpectNoError(err)

				checkCtx, cancel = context.WithDeadline(
					ctx,
					time.Now().Add(30*time.Second),
				)
				err = f.DevsyProviderOptionsCheckNamespaceDescription(
					checkCtx,
					"provider2",
					"Updated namespace parameter",
				)
				framework.ExpectNoError(err)
				cancel()

				err = f.DevsyProviderDelete(ctx, "provider2")
				framework.ExpectNoError(err)

				err = f.DevsyProviderUse(ctx, "provider2")
				framework.ExpectError(err)
			})

		ginkgo.It("should list all providers",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				tempDir, err := framework.CopyToTempDir(
					"tests/provider/testdata/simple-k8s-provider",
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				f := framework.NewDefaultFramework(initialDir + "/bin")

				err = f.DevsyProviderDelete(ctx, "provider1", "--ignore-not-found")
				framework.ExpectNoError(err)

				err = f.DevsyProviderAdd(ctx, tempDir+"/provider1.yaml")
				framework.ExpectNoError(err)

				err = f.DevsyProviderUse(ctx, "provider1")
				framework.ExpectNoError(err)

				err = os.WriteFile(tempDir+"/.DS_Store", []byte("test"), 0o644) // #nosec G306
				framework.ExpectNoError(err)

				err = f.DevsyProviderList(ctx)
				framework.ExpectNoError(err)

				err = f.DevsyProviderDelete(ctx, "provider1")
				framework.ExpectNoError(err)

				err = f.DevsyProviderUse(ctx, "provider1")
				framework.ExpectError(err)
			})

		ginkgo.It("should parse options",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				tempDir, err := framework.CopyToTempDir(
					"tests/provider/testdata/simple-k8s-provider",
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				f := framework.NewDefaultFramework(initialDir + "/bin")

				err = f.DevsyProviderDelete(ctx, "provider3", "--ignore-not-found")
				framework.ExpectNoError(err)

				podManifest := `
apiVersion: v1
kind: Pod
metadata:
	name: test
spec:
	containers:
	- name: devsy
`
				err = f.DevsyProviderAdd(
					ctx,
					tempDir+"/provider3.yaml",
					"--option=TEMPLATE="+podManifest,
				)
				framework.ExpectNoError(err)

				err = f.DevsyProviderUse(ctx, "provider3")
				framework.ExpectNoError(err)

				err = f.DevsyProviderFindOption(ctx, "provider3", podManifest)
				framework.ExpectNoError(err)

				err = f.DevsyProviderDelete(ctx, "provider3")
				framework.ExpectNoError(err)

				err = f.DevsyProviderUse(ctx, "provider3")
				framework.ExpectError(err)
			})

		// RENAME-1.
		ginkgo.It("should rename a provider to a new, valid name",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				tempDir, err := framework.CopyToTempDir(
					"tests/provider/testdata/simple-k8s-provider",
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				f := framework.NewDefaultFramework(initialDir + "/bin")

				providerName := "provider-rename1"
				renamedProviderName := "provider-renamed"

				// Ensure that provider is deleted.
				err = f.DevsyProviderDelete(ctx, providerName, "--ignore-not-found")
				framework.ExpectNoError(err)
				err = f.DevsyProviderDelete(ctx, renamedProviderName, "--ignore-not-found")
				framework.ExpectNoError(err)

				// Add provider.
				err = f.DevsyProviderAdd(ctx, tempDir+"/provider1.yaml", "--name", providerName)
				framework.ExpectNoError(err)

				err = f.DevsyProviderUse(ctx, providerName)
				framework.ExpectNoError(err)

				err = f.DevsyProviderRename(ctx, providerName, renamedProviderName)
				framework.ExpectNoError(err)

				err = f.DevsyProviderUse(ctx, providerName)
				framework.ExpectError(err)
				err = f.DevsyProviderUse(ctx, renamedProviderName)
				framework.ExpectNoError(err)

				err = f.DevsyProviderDelete(ctx, renamedProviderName)
				framework.ExpectNoError(err)
			})

		// RENAME-2.
		ginkgo.It(
			"should fail to rename a provider to a name that already exists",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				tempDir, err := framework.CopyToTempDir(
					"tests/provider/testdata/simple-k8s-provider",
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				f := framework.NewDefaultFramework(initialDir + "/bin")

				providerToRename := "provider-to-rename2"
				existingProvider := "existing-provider2"

				err = f.DevsyProviderDelete(ctx, providerToRename, "--ignore-not-found")
				framework.ExpectNoError(err)
				err = f.DevsyProviderDelete(ctx, existingProvider, "--ignore-not-found")
				framework.ExpectNoError(err)

				err = f.DevsyProviderAdd(
					ctx,
					tempDir+"/provider1.yaml",
					"--name",
					providerToRename,
				)
				framework.ExpectNoError(err)
				err = f.DevsyProviderAdd(
					ctx,
					tempDir+"/provider2.yaml",
					"--name",
					existingProvider,
				)
				framework.ExpectNoError(err)

				err = f.DevsyProviderRename(
					ctx,
					providerToRename,
					existingProvider,
				)
				framework.ExpectError(err)

				err = f.DevsyProviderUse(ctx, providerToRename)
				framework.ExpectNoError(err)
				err = f.DevsyProviderUse(ctx, existingProvider)
				framework.ExpectNoError(err)

				err = f.DevsyProviderDelete(ctx, providerToRename)
				framework.ExpectNoError(err)
				err = f.DevsyProviderDelete(ctx, existingProvider)
				framework.ExpectNoError(err)
			},
		)

		ginkgo.It("should fail to rename a non-existent provider",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")

				nonExistentProvider := "non-existent-provider3"
				newName := "new-name3"

				err := f.DevsyProviderDelete(ctx, nonExistentProvider, "--ignore-not-found")
				framework.ExpectNoError(err)

				// Attempt to rename non-existent provider.
				err = f.DevsyProviderRename(ctx, nonExistentProvider, newName)
				framework.ExpectError(err)
			})

		ginkgo.It(
			"should rename a provider with an associated running workspace",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")

				providerName := "provider-with-workspace4"
				renamedProviderName := "renamed-provider-with-workspace4"

				workspaceList, err := f.DevsyListParsed(ctx)
				framework.ExpectNoError(err)
				for _, ws := range workspaceList {
					if ws.Provider.Name == providerName {
						err = f.DevsyStop(ctx, ws.ID)
						framework.ExpectNoError(err)
						err = f.DevsyWorkspaceDelete(ctx, ws.ID)
						framework.ExpectNoError(err)
					}
				}

				err = f.DevsyProviderDelete(ctx, providerName, "--ignore-not-found")
				framework.ExpectNoError(err)
				err = f.DevsyProviderDelete(ctx, renamedProviderName, "--ignore-not-found")
				framework.ExpectNoError(err)

				tempDir, err := framework.CopyToTempDir("tests/up/testdata/no-devcontainer")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				err = addDockerProvider(ctx, f, providerName)
				framework.ExpectNoError(err)
				err = f.DevsyProviderUse(ctx, providerName)
				framework.ExpectNoError(err)

				err = f.DevsyUp(ctx, tempDir)
				framework.ExpectNoError(err)

				err = f.DevsyProviderRename(ctx, providerName, renamedProviderName)
				framework.ExpectNoError(err)

				err = f.DevsyProviderUse(ctx, providerName)
				framework.ExpectError(err)
				err = f.DevsyProviderUse(ctx, renamedProviderName)
				framework.ExpectNoError(err)

				err = f.DevsyUp(ctx, tempDir)
				framework.ExpectNoError(err)

				gomega.Eventually(func() string {
					status, err := f.DevsyStatus(ctx, tempDir)
					if err != nil {
						return "error"
					}
					return string(status.State)
				}).WithTimeout(30 * time.Second).
					WithPolling(1 * time.Second).
					Should(gomega.Equal("Running"))

				_, err = f.DevsySSH(ctx, tempDir, "echo 'hello'")
				framework.ExpectNoError(err)

				err = f.DevsyStop(ctx, tempDir)
				framework.ExpectNoError(err)
				err = f.DevsyWorkspaceDelete(ctx, tempDir)
				framework.ExpectNoError(err)

				workspaceID := workspace.ToID(tempDir)
				gomega.Eventually(func() error {
					_, err := f.FindWorkspace(ctx, workspaceID)
					return err
				}).WithTimeout(30 * time.Second).
					WithPolling(1 * time.Second).
					Should(gomega.HaveOccurred())

				err = f.DevsyProviderDelete(ctx, renamedProviderName)
				framework.ExpectNoError(err)
			},
		)

		ginkgo.It("should fail to rename a provider to an invalid name",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				tempDir, err := framework.CopyToTempDir(
					"tests/provider/testdata/simple-k8s-provider",
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				f := framework.NewDefaultFramework(initialDir + "/bin")

				providerName := "provider-to-rename-invalid6"

				err = f.DevsyProviderDelete(ctx, providerName, "--ignore-not-found")
				framework.ExpectNoError(err)

				err = f.DevsyProviderAdd(ctx, tempDir+"/provider1.yaml", "--name", providerName)
				framework.ExpectNoError(err)

				err = f.DevsyProviderRename(ctx, providerName, "invalid/name6")
				framework.ExpectError(err)

				err = f.DevsyProviderUse(ctx, providerName)
				framework.ExpectNoError(err)

				err = f.DevsyProviderDelete(ctx, providerName)
				framework.ExpectNoError(err)
			})

		ginkgo.It("should preserve provider options after rename",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")

				providerName := "provider-opts-rename7"
				renamedName := "renamed-opts-rename7"

				err := f.DevsyProviderDelete(ctx, providerName, "--ignore-not-found")
				framework.ExpectNoError(err)
				err = f.DevsyProviderDelete(ctx, renamedName, "--ignore-not-found")
				framework.ExpectNoError(err)

				err = addDockerProvider(ctx, f, providerName)
				framework.ExpectNoError(err)
				err = f.DevsyProviderUse(ctx, providerName)
				framework.ExpectNoError(err)

				beforeJSON, err := f.DevsyProviderOptionsJSON(ctx, providerName)
				framework.ExpectNoError(err)

				var beforeOpts map[string]any
				err = json.Unmarshal([]byte(beforeJSON), &beforeOpts)
				framework.ExpectNoError(err)

				err = f.DevsyProviderRename(ctx, providerName, renamedName)
				framework.ExpectNoError(err)

				afterJSON, err := f.DevsyProviderOptionsJSON(ctx, renamedName)
				framework.ExpectNoError(err)

				var afterOpts map[string]any
				err = json.Unmarshal([]byte(afterJSON), &afterOpts)
				framework.ExpectNoError(err)

				for key, beforeVal := range beforeOpts {
					afterVal, exists := afterOpts[key]
					gomega.Expect(exists).
						To(gomega.BeTrue(), "option %s should exist after rename", key)

					beforeMap := beforeVal.(map[string]any)
					afterMap := afterVal.(map[string]any)

					beforeV, hasBefore := beforeMap["value"]
					afterV, hasAfter := afterMap["value"]
					gomega.Expect(hasAfter).To(gomega.Equal(hasBefore),
						"option %s value presence should be preserved", key)
					if hasBefore {
						gomega.Expect(afterV).To(gomega.Equal(beforeV),
							"option %s value should be preserved", key)
					}
				}

				err = f.DevsyProviderUse(ctx, providerName)
				framework.ExpectError(err)

				err = f.DevsyProviderDelete(ctx, renamedName)
				framework.ExpectNoError(err)
			})
	})
