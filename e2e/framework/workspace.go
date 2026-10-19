package framework

import (
	"context"
	"os"

	"github.com/onsi/ginkgo/v2"
)

// SetupDockerWorkspace copies testdataPath to a temp dir and returns it with a
// docker-provider framework; temp dir and workspace are removed on cleanup.
func SetupDockerWorkspace(testdataPath, initialDir string) (string, *Framework, error) {
	tempDir, err := CopyToTempDir(testdataPath)
	if err != nil {
		return "", nil, err
	}

	f, err := SetupDockerProvider(initialDir+"/bin", "docker")
	if err != nil {
		return "", nil, err
	}

	ginkgo.DeferCleanup(CleanupTempDir, initialDir, tempDir)
	ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

	return tempDir, f, nil
}

// CopyTestdata copies testdataPath to a temp dir, changes into it, and registers
// cleanup that removes the temp dir and changes back to initialDir.
func CopyTestdata(testdataPath, initialDir string) string {
	tempDir, err := CopyToTempDir(testdataPath)
	ExpectNoErrorWithOffset(1, err)
	ginkgo.DeferCleanup(CleanupTempDir, initialDir, tempDir)

	return tempDir
}

// CopyTestdataNoChdir copies testdataPath to a temp dir and removes it on cleanup.
func CopyTestdataNoChdir(testdataPath string) string {
	tempDir, err := CopyToTempDirWithoutChdir(testdataPath)
	ExpectNoErrorWithOffset(1, err)
	ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

	return tempDir
}

// SetupDockerWorkspaceAndUp is SetupDockerWorkspace followed by devsy up.
func SetupDockerWorkspaceAndUp(
	ctx context.Context,
	testdataPath, initialDir string,
) (string, *Framework, error) {
	tempDir, f, err := SetupDockerWorkspace(testdataPath, initialDir)
	if err != nil {
		return "", nil, err
	}

	return tempDir, f, f.DevsyUp(ctx, tempDir)
}
