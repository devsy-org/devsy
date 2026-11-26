package rename

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	statusError      = "error"
	containerRunning = "running"
)

type workspaceSnapshot struct {
	id              string
	uid             string
	containerID     string
	containerStatus string
}

type presentation struct {
	label string
	alias string
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
				providerName := "rename-provider1"
				workspaceName := "rename-ws1"
				renamedWorkspaceName := "renamed-ws1"

				f := setupNoDevcontainerWorkspace(ctx, initialDir, providerName, workspaceName)

				gomega.Eventually(func() string {
					status, err := f.DevsyStatus(ctx, workspaceName)
					if err != nil {
						return statusError
					}
					return string(status.State)
				}).WithTimeout(30 * time.Second).
					WithPolling(1 * time.Second).
					Should(gomega.Equal("Running"))

				err := f.DevsyStop(ctx, workspaceName)
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
				providerName := "rename-provider2"
				workspaceName := "rename-ws2"
				renamedWorkspaceName := "renamed-ws2"

				f := setupNoDevcontainerWorkspace(ctx, initialDir, providerName, workspaceName)

				gomega.Eventually(func() string {
					status, err := f.DevsyStatus(ctx, workspaceName)
					if err != nil {
						return statusError
					}
					return string(status.State)
				}).WithTimeout(30 * time.Second).
					WithPolling(1 * time.Second).
					Should(gomega.Equal("Running"))

				// Rename while running - should auto-stop
				err := f.DevsyRename(ctx, workspaceName, renamedWorkspaceName)
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

				tempDir := framework.CopyTestdata("tests/up/testdata/no-devcontainer", initialDir)

				err = f.DevsyProviderAddDocker(ctx, providerName)
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
				providerName := "rename-provider5"
				workspaceName := "rename-ws5"

				f := setupNoDevcontainerWorkspace(ctx, initialDir, providerName, workspaceName)
				err := f.DevsyStop(ctx, workspaceName)
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
				providerName := "rename-provider6"
				workspaceName := "rename-ws6"
				renamedWorkspaceName := "renamed-ws6"

				f := setupNoDevcontainerWorkspace(ctx, initialDir, providerName, workspaceName)
				err := f.DevsyStop(ctx, workspaceName)
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

		ginkgo.It("should edit label and ssh alias of a running workspace without recreating it",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				providerName := "rename-provider7"
				workspaceName := "rename-ws7"
				otherWorkspaceName := "rename-ws7b"

				f := setupNoDevcontainerWorkspace(ctx, initialDir, providerName, workspaceName)
				before, err := f.FindWorkspace(ctx, workspaceName)
				framework.ExpectNoError(err)

				err = f.DevsyWorkspaceEdit(ctx, workspaceName,
					"--display-name", "My Horse Social Network", "--ssh-host-alias", "my-project")
				framework.ExpectNoError(err)

				after, err := f.FindWorkspace(ctx, workspaceName)
				framework.ExpectNoError(err)
				gomega.Expect(after.ID).To(gomega.Equal(workspaceName))
				gomega.Expect(after.UID).To(gomega.Equal(before.UID))
				gomega.Expect(after.DisplayName).To(gomega.Equal("My Horse Social Network"))
				gomega.Expect(after.SSHHostAlias).To(gomega.Equal("my-project"))

				status, err := f.DevsyStatus(ctx, workspaceName)
				framework.ExpectNoError(err)
				gomega.Expect(string(status.State)).To(gomega.Equal("Running"))

				err = f.DevsyUp(ctx, before.Source.LocalFolder, "--id", otherWorkspaceName)
				framework.ExpectNoError(err)

				err = f.DevsyWorkspaceEdit(ctx, otherWorkspaceName,
					"--display-name", "my horse social network")
				framework.ExpectError(err)
				err = f.DevsyWorkspaceEdit(
					ctx,
					otherWorkspaceName,
					"--ssh-host-alias",
					"my-project",
				)
				framework.ExpectError(err)
				err = f.DevsyWorkspaceEdit(ctx, workspaceName, "--ssh-host-alias", "")
				framework.ExpectNoError(err)

				err = f.DevsyWorkspaceDelete(ctx, otherWorkspaceName)
				framework.ExpectNoError(err)
				err = f.DevsyWorkspaceDelete(ctx, workspaceName)
				framework.ExpectNoError(err)
				err = f.DevsyProviderDelete(ctx, providerName)
				framework.ExpectNoError(err)
			})

		ginkgo.It("should persist label and alias across CLI processes while keeping identity",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				providerName := "rename-provider8"
				workspaceName := "rename-ws8"
				first := presentation{label: "Customer Project", alias: "customer-project"}
				labelOnly := presentation{label: "Customer Project Two", alias: first.alias}
				aliasOnly := presentation{label: labelOnly.label, alias: "customer-project-two"}

				f := setupNoDevcontainerWorkspace(ctx, initialDir, providerName, workspaceName)
				before := snapshotWorkspace(ctx, f, workspaceName)
				gomega.Expect(before.containerStatus).To(gomega.Equal(containerRunning))
				expectPresentation(ctx, f, workspaceName, presentation{})

				result, err := f.DevsyWorkspaceEditResult(ctx, workspaceName,
					"--display-name", first.label, "--ssh-host-alias", first.alias)
				framework.ExpectNoError(err)
				gomega.Expect(result.ID).To(gomega.Equal(before.id))
				gomega.Expect(result.UID).To(gomega.Equal(before.uid))
				gomega.Expect(result.DisplayName).To(gomega.Equal(first.label))
				gomega.Expect(result.SSHHostAlias).To(gomega.Equal(first.alias))
				expectPresentation(ctx, f, workspaceName, first)
				expectStable(ctx, f, before)

				err = f.DevsyWorkspaceEdit(ctx, workspaceName, "--display-name", labelOnly.label)
				framework.ExpectNoError(err)
				expectPresentation(ctx, f, workspaceName, labelOnly)
				expectStable(ctx, f, before)

				err = f.DevsyWorkspaceEdit(ctx, workspaceName, "--ssh-host-alias", aliasOnly.alias)
				framework.ExpectNoError(err)
				expectPresentation(ctx, f, workspaceName, aliasOnly)
				expectStable(ctx, f, before)

				cleanupWorkspaces(ctx, f, providerName, workspaceName)
			})

		ginkgo.It("should reject duplicate labels and aliases and keep the first workspace intact",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				providerName := "rename-provider9"
				workspaceName := "rename-ws9"
				otherWorkspaceName := "rename-ws9b"
				owner := presentation{label: "Collision Label", alias: "collision-alias"}

				f := setupNoDevcontainerWorkspace(ctx, initialDir, providerName, workspaceName)
				first := snapshotWorkspace(ctx, f, workspaceName)
				folder := localFolder(ctx, f, workspaceName)
				err := f.DevsyUp(ctx, folder, "--id", otherWorkspaceName)
				framework.ExpectNoError(err)
				second := snapshotWorkspace(ctx, f, otherWorkspaceName)

				err = f.DevsyWorkspaceEdit(ctx, workspaceName,
					"--display-name", owner.label, "--ssh-host-alias", owner.alias)
				framework.ExpectNoError(err)

				expectEditRejected(ctx, f, otherWorkspaceName, "--display-name", owner.label)
				expectEditRejected(ctx, f, otherWorkspaceName, "--ssh-host-alias", owner.alias)
				expectEditRejected(ctx, f, otherWorkspaceName, "--ssh-host-alias", workspaceName)
				expectEditRejected(ctx, f, otherWorkspaceName,
					"--display-name", "Unique Label", "--ssh-host-alias", owner.alias)

				expectPresentation(ctx, f, workspaceName, owner)
				expectPresentation(ctx, f, otherWorkspaceName, presentation{})
				expectStable(ctx, f, first)
				expectStable(ctx, f, second)

				cleanupWorkspaces(ctx, f, providerName, otherWorkspaceName, workspaceName)
			})

		ginkgo.It("should let exactly one of two simultaneous CLI processes claim an alias",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				providerName := "rename-provider10"
				workspaceName := "rename-ws10"
				otherWorkspaceName := "rename-ws10b"
				alias := "race-alias"

				f := setupNoDevcontainerWorkspace(ctx, initialDir, providerName, workspaceName)
				folder := localFolder(ctx, f, workspaceName)
				err := f.DevsyUp(ctx, folder, "--id", otherWorkspaceName)
				framework.ExpectNoError(err)

				ids := []string{workspaceName, otherWorkspaceName}
				errs := editAliasConcurrently(ctx, f, ids, alias)

				winners := succeededIDs(ids, errs)
				gomega.Expect(winners).To(gomega.HaveLen(1), "errors: %v", errs)
				gomega.Expect(aliasOwners(ctx, f, alias)).To(gomega.ConsistOf(winners))

				cleanupWorkspaces(ctx, f, providerName, otherWorkspaceName, workspaceName)
			})

		ginkgo.It("should reject an edit pinned to the UID of a deleted and recreated workspace",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				providerName := "rename-provider11"
				workspaceName := "rename-ws11"

				f := setupNoDevcontainerWorkspace(ctx, initialDir, providerName, workspaceName)
				old := snapshotWorkspace(ctx, f, workspaceName)
				folder := localFolder(ctx, f, workspaceName)

				err := f.DevsyWorkspaceDelete(ctx, workspaceName)
				framework.ExpectNoError(err)
				waitWorkspaceGone(ctx, f, workspaceName)

				err = f.DevsyUp(ctx, folder, "--id", workspaceName)
				framework.ExpectNoError(err)
				recreated := snapshotWorkspace(ctx, f, workspaceName)
				gomega.Expect(recreated.uid).NotTo(gomega.Equal(old.uid))

				expectEditRejected(ctx, f, workspaceName,
					"--expected-uid", old.uid, "--display-name", "Stale Label")
				expectPresentation(ctx, f, workspaceName, presentation{})

				err = f.DevsyWorkspaceEdit(ctx, workspaceName,
					"--expected-uid", recreated.uid, "--display-name", "Fresh Label")
				framework.ExpectNoError(err)
				expectPresentation(ctx, f, workspaceName, presentation{label: "Fresh Label"})

				cleanupWorkspaces(ctx, f, providerName, workspaceName)
			})

		ginkgo.It("should keep label and alias when the workspace ID is renamed",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				providerName := "rename-provider12"
				workspaceName := "rename-ws12"
				renamedWorkspaceName := "renamed-ws12"
				want := presentation{label: "Renamed Project", alias: "renamed-project"}

				f := setupNoDevcontainerWorkspace(ctx, initialDir, providerName, workspaceName)
				before, err := f.FindWorkspace(ctx, workspaceName)
				framework.ExpectNoError(err)
				err = f.DevsyWorkspaceEdit(ctx, workspaceName,
					"--display-name", want.label, "--ssh-host-alias", want.alias)
				framework.ExpectNoError(err)
				err = f.DevsyStop(ctx, workspaceName)
				framework.ExpectNoError(err)

				err = f.DevsyRename(ctx, workspaceName, renamedWorkspaceName)
				framework.ExpectNoError(err)

				after, err := f.FindWorkspace(ctx, renamedWorkspaceName)
				framework.ExpectNoError(err)
				gomega.Expect(after.UID).To(gomega.Equal(before.UID))
				expectPresentation(ctx, f, renamedWorkspaceName, want)

				cleanupWorkspaces(ctx, f, providerName, renamedWorkspaceName)
			})
	})

func setupNoDevcontainerWorkspace(
	ctx context.Context,
	initialDir, providerName, workspaceName string,
) *framework.Framework {
	f := framework.NewDefaultFramework(initialDir + "/bin")

	err := f.DevsyProviderDelete(ctx, providerName, "--ignore-not-found")
	framework.ExpectNoError(err)

	tempDir := framework.CopyTestdata("tests/up/testdata/no-devcontainer", initialDir)

	err = f.DevsyProviderAddDocker(ctx, providerName)
	framework.ExpectNoError(err)
	err = f.DevsyProviderUse(ctx, providerName)
	framework.ExpectNoError(err)

	err = f.DevsyUp(ctx, tempDir, "--id", workspaceName)
	framework.ExpectNoError(err)

	return f
}

func cleanupWorkspaces(
	ctx context.Context,
	f *framework.Framework,
	providerName string,
	workspaceNames ...string,
) {
	for _, name := range workspaceNames {
		framework.ExpectNoError(f.DevsyWorkspaceDelete(ctx, name))
	}
	framework.ExpectNoError(f.DevsyProviderDelete(ctx, providerName))
}

func localFolder(ctx context.Context, f *framework.Framework, id string) string {
	ws, err := f.FindWorkspace(ctx, id)
	framework.ExpectNoError(err)
	return ws.Source.LocalFolder
}

func snapshotWorkspace(
	ctx context.Context,
	f *framework.Framework,
	id string,
) workspaceSnapshot {
	ws, err := f.FindWorkspace(ctx, id)
	framework.ExpectNoError(err)

	helper := &docker.DockerHelper{DockerCommand: "docker"}
	container, err := helper.FindDevContainer(ctx, []string{
		fmt.Sprintf("%s=%s", pkgconfig.DevcontainerIDLabel, ws.UID),
	})
	framework.ExpectNoError(err)
	gomega.Expect(container).NotTo(gomega.BeNil())

	return workspaceSnapshot{
		id:              ws.ID,
		uid:             ws.UID,
		containerID:     container.ID,
		containerStatus: string(container.State.Status),
	}
}

func expectStable(ctx context.Context, f *framework.Framework, before workspaceSnapshot) {
	gomega.Expect(snapshotWorkspace(ctx, f, before.id)).To(gomega.Equal(before))

	status, err := f.DevsyStatus(ctx, before.id)
	framework.ExpectNoError(err)
	gomega.Expect(string(status.State)).To(gomega.Equal("Running"))
}

func expectPresentation(
	ctx context.Context,
	f *framework.Framework,
	id string,
	want presentation,
) {
	ws, err := f.FindWorkspace(ctx, id)
	framework.ExpectNoError(err)
	gomega.Expect(ws.DisplayName).To(gomega.Equal(want.label))
	gomega.Expect(ws.SSHHostAlias).To(gomega.Equal(want.alias))
}

func expectEditRejected(
	ctx context.Context,
	f *framework.Framework,
	id string,
	args ...string,
) {
	err := f.DevsyWorkspaceEdit(ctx, id, args...)
	framework.ExpectError(err, "edit %s %v must be rejected", id, args)
}

func editAliasConcurrently(
	ctx context.Context,
	f *framework.Framework,
	ids []string,
	alias string,
) []error {
	errs := make([]error, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Go(func() {
			errs[i] = f.DevsyWorkspaceEdit(ctx, id, "--ssh-host-alias", alias)
		})
	}
	wg.Wait()

	return errs
}

func aliasOwners(ctx context.Context, f *framework.Framework, alias string) []string {
	list, err := f.DevsyListParsed(ctx)
	framework.ExpectNoError(err)

	owners := []string{}
	for _, ws := range list {
		if ws.SSHHostAlias == alias {
			owners = append(owners, ws.ID)
		}
	}

	return owners
}

func waitWorkspaceGone(ctx context.Context, f *framework.Framework, id string) {
	gomega.Eventually(func() error {
		_, err := f.FindWorkspace(ctx, id)
		return err
	}).WithTimeout(30 * time.Second).
		WithPolling(1 * time.Second).
		Should(gomega.HaveOccurred())
}

func succeededIDs(ids []string, errs []error) []string {
	succeeded := []string{}
	for i, err := range errs {
		if err == nil {
			succeeded = append(succeeded, ids[i])
		}
	}

	return succeeded
}
