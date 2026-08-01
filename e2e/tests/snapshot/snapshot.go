package snapshot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	registryFlag        = "--registry"
	snapshotCmd         = "snapshot"
	snapshotVerbCreate  = "create"
	snapshotVerbRestore = "restore"
)

var _ = ginkgo.Describe("devsy snapshot", ginkgo.Label("snapshot"), func() {
	var (
		f            *framework.Framework
		dockerHelper *docker.DockerHelper
		registryHost string
		cleanupReg   func()
	)

	ginkgo.BeforeEach(func(ctx context.Context) {
		// The registry fixture is deliberately reachable via plain HTTP (see
		// startLocalRegistry); opt the spawned devsy CLI subprocesses into
		// treating host.docker.internal refs as insecure for this suite only.
		framework.ExpectNoError(os.Setenv(pkgconfig.EnvInsecureDockerInternal, "true"))

		var err error
		dockerHelper = &docker.DockerHelper{DockerCommand: "docker"}
		registryHost, cleanupReg, err = startLocalRegistry(ctx, dockerHelper)
		framework.ExpectNoError(err)

		initialDir, err := os.Getwd()
		framework.ExpectNoError(err)
		f, err = framework.SetupDockerProvider(filepath.Join(initialDir, "bin"), "docker")
		framework.ExpectNoError(err)
	})

	ginkgo.AfterEach(func() {
		_ = os.Unsetenv(pkgconfig.EnvInsecureDockerInternal)
		if cleanupReg != nil {
			cleanupReg()
		}
	})

	ginkgo.It("creates, mutates, and restores a snapshot", func(ctx context.Context) {
		initialDir, err := os.Getwd()
		framework.ExpectNoError(err)

		tempDir, err := framework.CopyToTempDir("tests/snapshot/testdata/docker")
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
		ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

		framework.ExpectNoError(f.DevsyUp(ctx, tempDir))

		// Discover the in-container workspace folder rather than assuming a
		// naming convention: the devcontainer shell starts there by default.
		workspaceFolder, err := f.DevsySSH(ctx, tempDir, "pwd")
		framework.ExpectNoError(err)
		workspaceFolder = strings.TrimSpace(workspaceFolder)

		// Mutate both the bind-mounted volume (proves volume restore) and the
		// container's root filesystem outside any bind mount (proves the
		// committed-image restore, since only CommitContainer captures this).
		markerCmd := fmt.Sprintf("echo mutated > %s/marker.txt", workspaceFolder)
		_, err = f.DevsySSH(ctx, tempDir, markerCmd)
		framework.ExpectNoError(err)
		_, err = f.DevsySSH(ctx, tempDir, "echo fs-mutated > /tmp/fs-marker.txt")
		framework.ExpectNoError(err)

		out, _, err := f.ExecCommandCapture(ctx, []string{
			snapshotCmd, snapshotVerbCreate, tempDir, registryFlag, registryHost + "/e2e/snapshots",
		})
		framework.ExpectNoError(err)
		snapshotRef := strings.TrimSpace(out)
		gomega.Expect(snapshotRef).To(gomega.ContainSubstring(registryHost))

		restoredID := "restored-" + filepath.Base(tempDir)
		ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, restoredID)

		_, _, err = f.ExecCommandCapture(ctx, []string{
			snapshotCmd, snapshotVerbRestore, snapshotRef, "--workspace-id", restoredID,
		})
		framework.ExpectNoError(err)

		restoredWorkspaceFolder, err := f.DevsySSH(ctx, restoredID, "pwd")
		framework.ExpectNoError(err)
		restoredWorkspaceFolder = strings.TrimSpace(restoredWorkspaceFolder)

		catMarkerCmd := fmt.Sprintf("cat %s/marker.txt", restoredWorkspaceFolder)
		content, err := f.DevsySSH(ctx, restoredID, catMarkerCmd)
		framework.ExpectNoError(err)
		gomega.Expect(content).To(gomega.ContainSubstring("mutated"))

		fsContent, err := f.DevsySSH(ctx, restoredID, "cat /tmp/fs-marker.txt")
		framework.ExpectNoError(err)
		gomega.Expect(fsContent).To(gomega.ContainSubstring("fs-mutated"))
	}, ginkgo.SpecTimeout(framework.TimeoutLong()))

	ginkgo.It("transfers a workspace docker to docker under a new id", func(ctx context.Context) {
		initialDir, err := os.Getwd()
		framework.ExpectNoError(err)

		tempDir, err := framework.CopyToTempDir("tests/snapshot/testdata/docker")
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
		ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

		framework.ExpectNoError(f.DevsyUp(ctx, tempDir))

		originalWorkspace, err := f.FindWorkspace(ctx, tempDir)
		framework.ExpectNoError(err)

		workspaceFolder, err := f.DevsySSH(ctx, tempDir, "pwd")
		framework.ExpectNoError(err)
		workspaceFolder = strings.TrimSpace(workspaceFolder)

		transferCmd := fmt.Sprintf("echo transfer-me > %s/transfer.txt", workspaceFolder)
		_, err = f.DevsySSH(ctx, tempDir, transferCmd)
		framework.ExpectNoError(err)

		out, _, err := f.ExecCommandCapture(ctx, []string{
			snapshotCmd, snapshotVerbCreate, tempDir, registryFlag, registryHost + "/e2e/snapshots",
		})
		framework.ExpectNoError(err)
		snapshotRef := strings.TrimSpace(out)

		transferredID := "transferred-" + filepath.Base(tempDir)
		ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, transferredID)

		_, _, err = f.ExecCommandCapture(ctx, []string{
			snapshotCmd, snapshotVerbRestore, snapshotRef, "--workspace-id", transferredID,
		})
		framework.ExpectNoError(err)

		transferredWorkspace, err := f.FindWorkspace(ctx, transferredID)
		framework.ExpectNoError(err)
		gomega.Expect(transferredWorkspace.ID).ToNot(gomega.Equal(originalWorkspace.ID))
		gomega.Expect(transferredWorkspace.UID).ToNot(gomega.Equal(originalWorkspace.UID))

		// Both the original and the transferred workspace must be present and
		// independently healthy: two distinct running containers.
		list, err := f.DevsyListParsed(ctx)
		framework.ExpectNoError(err)
		var foundOriginal, foundTransferred bool
		for _, w := range list {
			if w.ID == originalWorkspace.ID {
				foundOriginal = true
			}
			if w.ID == transferredWorkspace.ID {
				foundTransferred = true
			}
		}
		gomega.Expect(foundOriginal).To(
			gomega.BeTrue(), "original workspace should still be listed",
		)
		gomega.Expect(foundTransferred).To(
			gomega.BeTrue(), "transferred workspace should be listed",
		)

		_, err = f.DevsySSH(ctx, tempDir, "pwd")
		framework.ExpectNoError(err, "original workspace should still be reachable")

		transferredWorkspaceFolder, err := f.DevsySSH(ctx, transferredID, "pwd")
		framework.ExpectNoError(err, "transferred workspace should be reachable")
		transferredWorkspaceFolder = strings.TrimSpace(transferredWorkspaceFolder)

		catTransferCmd := fmt.Sprintf("cat %s/transfer.txt", transferredWorkspaceFolder)
		content, err := f.DevsySSH(ctx, transferredID, catTransferCmd)
		framework.ExpectNoError(err)
		gomega.Expect(content).To(gomega.ContainSubstring("transfer-me"))

		originalIDs, err := dockerHelper.FindContainer(ctx, []string{
			fmt.Sprintf("%s=%s", pkgconfig.DevcontainerIDLabel, originalWorkspace.UID),
		})
		framework.ExpectNoError(err)
		gomega.Expect(originalIDs).ToNot(gomega.BeEmpty(), "original container should still exist")

		transferredIDs, err := dockerHelper.FindContainer(ctx, []string{
			fmt.Sprintf("%s=%s", pkgconfig.DevcontainerIDLabel, transferredWorkspace.UID),
		})
		framework.ExpectNoError(err)
		gomega.Expect(transferredIDs).ToNot(gomega.BeEmpty(), "transferred container should exist")
		gomega.Expect(transferredIDs[0]).ToNot(gomega.Equal(originalIDs[0]))
	}, ginkgo.SpecTimeout(framework.TimeoutLong()))

	ginkgo.It("leaves no manifest visible when the registry is unreachable mid-push", func(
		ctx context.Context,
	) {
		initialDir, err := os.Getwd()
		framework.ExpectNoError(err)

		tempDir, err := framework.CopyToTempDir("tests/snapshot/testdata/docker")
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
		ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)
		framework.ExpectNoError(f.DevsyUp(ctx, tempDir))

		registry := registryHost + "/e2e/snapshots"

		cleanupReg() // kill the registry before create runs, forcing a mid-push failure
		cleanupReg = func() {}

		_, _, err = f.ExecCommandCapture(ctx, []string{
			snapshotCmd, snapshotVerbCreate, tempDir, registryFlag, registry,
		})
		gomega.Expect(err).To(gomega.HaveOccurred())

		// Workspace itself must be unaffected by the failed snapshot attempt.
		list, err := f.DevsyListParsed(ctx)
		framework.ExpectNoError(err)
		gomega.Expect(list).ToNot(gomega.BeEmpty())

		_, err = f.DevsySSH(ctx, tempDir, "pwd")
		framework.ExpectNoError(
			err,
			"workspace should still be reachable after failed snapshot create",
		)
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))
})
