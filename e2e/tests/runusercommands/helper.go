package runusercommands

import (
	"context"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
)

func setupWorkspace(testdataPath, initialDir string) (string, *framework.Framework, error) {
	tempDir, err := framework.CopyToTempDir(testdataPath)
	if err != nil {
		return "", nil, err
	}

	f, err := framework.SetupDockerProvider(initialDir+"/bin", "docker")
	if err != nil {
		return "", nil, err
	}

	ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
	ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

	return tempDir, f, nil
}

func setupWorkspaceAndUp(
	ctx context.Context,
	testdataPath, initialDir string,
) (string, *framework.Framework, error) {
	tempDir, f, err := setupWorkspace(testdataPath, initialDir)
	if err != nil {
		return "", nil, err
	}

	return tempDir, f, f.DevsyUp(ctx, tempDir)
}
