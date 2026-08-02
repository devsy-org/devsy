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
	debugFlag           = "--debug"
	workspaceIDFlag     = "--workspace-id"
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
			debugFlag,
		})
		framework.ExpectNoError(err)
		snapshotRef := strings.TrimSpace(out)
		gomega.Expect(snapshotRef).To(gomega.ContainSubstring(registryHost))

		restoredID := "restored-" + filepath.Base(tempDir)
		ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, restoredID)

		_, _, err = f.ExecCommandCapture(ctx, []string{
			snapshotCmd, snapshotVerbRestore, snapshotRef, workspaceIDFlag, restoredID, debugFlag,
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
			debugFlag,
		})
		framework.ExpectNoError(err)
		snapshotRef := strings.TrimSpace(out)

		transferredID := "transferred-" + filepath.Base(tempDir)
		ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, transferredID)

		_, _, err = f.ExecCommandCapture(ctx, []string{
			snapshotCmd, snapshotVerbRestore, snapshotRef,
			workspaceIDFlag, transferredID, debugFlag,
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

	ginkgo.It(
		"fails create without disturbing the workspace when the registry is unreachable mid-push",
		func(ctx context.Context) {
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
				snapshotCmd, snapshotVerbCreate, tempDir, registryFlag, registry, debugFlag,
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
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)

	ginkgo.It("rejects snapshot create for a workspace with more than one mount", func(
		ctx context.Context,
	) {
		initialDir, err := os.Getwd()
		framework.ExpectNoError(err)

		tempDir, err := framework.CopyToTempDir("tests/snapshot/testdata/docker-multi-mount")
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
		ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)
		framework.ExpectNoError(f.DevsyUp(ctx, tempDir))

		_, stderr, err := f.ExecCommandCapture(ctx, []string{
			snapshotCmd, snapshotVerbCreate, tempDir, registryFlag,
			registryHost + "/e2e/snapshots", debugFlag,
		})
		gomega.Expect(err).To(gomega.HaveOccurred())
		gomega.Expect(stderr).To(gomega.ContainSubstring("does not yet support multiple mounts"))

		// The rejected create must not have left the workspace itself broken.
		_, err = f.DevsySSH(ctx, tempDir, "pwd")
		framework.ExpectNoError(
			err,
			"workspace should still be reachable after a rejected multi-mount snapshot create",
		)
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("re-restoring the same snapshot into an already-populated workspace is a no-op", func(
		ctx context.Context,
	) {
		initialDir, err := os.Getwd()
		framework.ExpectNoError(err)

		tempDir, err := framework.CopyToTempDir("tests/snapshot/testdata/docker")
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
		ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)
		framework.ExpectNoError(f.DevsyUp(ctx, tempDir))

		workspaceFolder, err := f.DevsySSH(ctx, tempDir, "pwd")
		framework.ExpectNoError(err)
		workspaceFolder = strings.TrimSpace(workspaceFolder)

		markerCmd := fmt.Sprintf("echo mutated > %s/marker.txt", workspaceFolder)
		_, err = f.DevsySSH(ctx, tempDir, markerCmd)
		framework.ExpectNoError(err)

		out, _, err := f.ExecCommandCapture(ctx, []string{
			snapshotCmd, snapshotVerbCreate, tempDir, registryFlag, registryHost + "/e2e/snapshots",
			debugFlag,
		})
		framework.ExpectNoError(err)
		snapshotRef := strings.TrimSpace(out)

		restoredID := "rerestore-" + filepath.Base(tempDir)
		ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, restoredID)

		restoreArgs := []string{
			snapshotCmd, snapshotVerbRestore, snapshotRef, workspaceIDFlag, restoredID, debugFlag,
		}
		_, _, err = f.ExecCommandCapture(ctx, restoreArgs)
		framework.ExpectNoError(err)

		restoredWorkspaceFolder, err := f.DevsySSH(ctx, restoredID, "pwd")
		framework.ExpectNoError(err)
		restoredWorkspaceFolder = strings.TrimSpace(restoredWorkspaceFolder)

		// Mutate the restored workspace's own copy after the first restore, so
		// a second restore that (incorrectly) re-extracted the volume would
		// clobber this and a second restore that (correctly) skips would not.
		mutateAfterFirstRestore := fmt.Sprintf(
			"echo mutated-after-first-restore > %s/post-restore.txt", restoredWorkspaceFolder,
		)
		_, err = f.DevsySSH(ctx, restoredID, mutateAfterFirstRestore)
		framework.ExpectNoError(err)

		// Restoring the same ref into the same, now-populated workspace ID must
		// not touch existing content: it should be treated as already-restored
		// (skipSnapshotRestore) rather than re-extracting the volumes archive.
		_, _, err = f.ExecCommandCapture(ctx, restoreArgs)
		framework.ExpectNoError(err)

		content, err := f.DevsySSH(
			ctx, restoredID, fmt.Sprintf("cat %s/post-restore.txt", restoredWorkspaceFolder),
		)
		framework.ExpectNoError(
			err, "second restore into the same workspace id must not have wiped existing content",
		)
		gomega.Expect(content).To(gomega.ContainSubstring("mutated-after-first-restore"))

		markerContent, err := f.DevsySSH(
			ctx, restoredID, fmt.Sprintf("cat %s/marker.txt", restoredWorkspaceFolder),
		)
		framework.ExpectNoError(err)
		gomega.Expect(markerContent).To(gomega.ContainSubstring("mutated"))
	}, ginkgo.SpecTimeout(framework.TimeoutLong()))

	ginkgo.It("--reset forces a real restore over a non-empty target workspace", func(
		ctx context.Context,
	) {
		initialDir, err := os.Getwd()
		framework.ExpectNoError(err)

		tempDir, err := framework.CopyToTempDir("tests/snapshot/testdata/docker")
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
		ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)
		framework.ExpectNoError(f.DevsyUp(ctx, tempDir))

		workspaceFolder, err := f.DevsySSH(ctx, tempDir, "pwd")
		framework.ExpectNoError(err)
		workspaceFolder = strings.TrimSpace(workspaceFolder)

		markerCmd := fmt.Sprintf("echo mutated > %s/marker.txt", workspaceFolder)
		_, err = f.DevsySSH(ctx, tempDir, markerCmd)
		framework.ExpectNoError(err)

		out, _, err := f.ExecCommandCapture(ctx, []string{
			snapshotCmd, snapshotVerbCreate, tempDir, registryFlag, registryHost + "/e2e/snapshots",
			debugFlag,
		})
		framework.ExpectNoError(err)
		snapshotRef := strings.TrimSpace(out)

		resetID := "reset-" + filepath.Base(tempDir)
		ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, resetID)

		// devsy up --from-snapshot into a workspace id that doesn't exist yet
		// creates it fresh, exactly like `snapshot restore` would.
		_, _, err = f.ExecCommandCapture(ctx, []string{
			"workspace", "up", "--from-snapshot", snapshotRef,
			"--id", resetID, "--ide", "none", debugFlag,
		})
		framework.ExpectNoError(err)

		resetWorkspaceFolder, err := f.DevsySSH(ctx, resetID, "pwd")
		framework.ExpectNoError(err)
		resetWorkspaceFolder = strings.TrimSpace(resetWorkspaceFolder)

		// Add content the snapshot does not know about, then --reset: the
		// restore must run again and the pre-reset addition must not survive,
		// proving the volume was genuinely re-extracted rather than skipped.
		staleCmd := fmt.Sprintf("echo stale > %s/stale.txt", resetWorkspaceFolder)
		_, err = f.DevsySSH(ctx, resetID, staleCmd)
		framework.ExpectNoError(err)

		_, _, err = f.ExecCommandCapture(ctx, []string{
			"workspace", "up", "--from-snapshot", snapshotRef,
			"--id", resetID, "--ide", "none", "--reset", debugFlag,
		})
		framework.ExpectNoError(err)

		_, err = f.DevsySSH(ctx, resetID, fmt.Sprintf("cat %s/stale.txt", resetWorkspaceFolder))
		gomega.Expect(err).To(
			gomega.HaveOccurred(), "stale.txt should not survive a --reset restore",
		)

		markerContent, err := f.DevsySSH(
			ctx, resetID, fmt.Sprintf("cat %s/marker.txt", resetWorkspaceFolder),
		)
		framework.ExpectNoError(err)
		gomega.Expect(markerContent).To(gomega.ContainSubstring("mutated"))
	}, ginkgo.SpecTimeout(framework.TimeoutLong()))

	ginkgo.It("preserves a non-add-host runArg from the original devcontainer on restore", func(
		ctx context.Context,
	) {
		initialDir, err := os.Getwd()
		framework.ExpectNoError(err)

		tempDir, err := framework.CopyToTempDir("tests/snapshot/testdata/docker-runargs")
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
		ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)
		framework.ExpectNoError(f.DevsyUp(ctx, tempDir))

		out, _, err := f.ExecCommandCapture(ctx, []string{
			snapshotCmd, snapshotVerbCreate, tempDir, registryFlag, registryHost + "/e2e/snapshots",
			debugFlag,
		})
		framework.ExpectNoError(err)
		snapshotRef := strings.TrimSpace(out)

		restoredID := "runargs-" + filepath.Base(tempDir)
		ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, restoredID)

		_, _, err = f.ExecCommandCapture(ctx, []string{
			snapshotCmd, snapshotVerbRestore, snapshotRef, workspaceIDFlag, restoredID, debugFlag,
		})
		framework.ExpectNoError(err)

		restoredWorkspace, err := f.FindWorkspace(ctx, restoredID)
		framework.ExpectNoError(err)

		// The custom --label runArg only exists in this fixture's
		// devcontainer.json, not in the base image or --add-host (which the
		// registry fixture itself already depends on to function at all): its
		// presence on the restored container proves restore replays the
		// original runArgs generally, not just the one the test harness needs.
		containerIDs, err := dockerHelper.FindContainer(ctx, []string{
			fmt.Sprintf("%s=%s", pkgconfig.DevcontainerIDLabel, restoredWorkspace.UID),
			"devsy-e2e-snapshot-runargs=true",
		})
		framework.ExpectNoError(err)
		gomega.Expect(containerIDs).ToNot(
			gomega.BeEmpty(),
			"restored container should carry the original devcontainer.json's custom runArg label",
		)
	}, ginkgo.SpecTimeout(framework.TimeoutLong()))
})
