package up

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	docker "github.com/devsy-org/devsy/pkg/docker"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/log"
	"github.com/devsy-org/log/scanner"
)

type baseTestContext struct {
	f            *framework.Framework
	dockerHelper *docker.DockerHelper
	initialDir   string
}

func (btc *baseTestContext) execSSHCapture(
	ctx context.Context,
	projectName, command string,
) (string, error) {
	output, _, err := btc.f.ExecCommandCapture(
		ctx,
		[]string{"ssh", "--command", command, projectName},
	)
	return output, err
}

func (btc *baseTestContext) execSSH(ctx context.Context, tempDir, command string) (string, error) {
	return btc.f.DevsySSH(ctx, tempDir, command)
}

type dockerTestContext struct {
	baseTestContext
}

func (dtc *dockerTestContext) setupAndUp(
	ctx context.Context,
	testDataPath string,
	upArgs ...string,
) (string, error) {
	return setupWorkspaceAndUp(ctx, testDataPath, dtc.initialDir, dtc.f, upArgs...)
}

func (dtc *dockerTestContext) findWorkspaceContainer(
	ctx context.Context,
	workspace *provider2.Workspace,
) ([]string, error) {
	return dtc.dockerHelper.FindContainer(
		ctx,
		[]string{fmt.Sprintf("%s=%s", config.DockerIDLabel, workspace.UID)},
	)
}

// Log scanning functions.
func findMessage(reader io.Reader, message string) error {
	scan := scanner.NewScanner(reader)
	for scan.Scan() {
		if line := scan.Bytes(); len(line) > 0 {
			lineObject := &log.Line{}
			if err := json.Unmarshal(
				line,
				lineObject,
			); err == nil &&
				strings.Contains(lineObject.Message, message) {
				return nil
			}
		}
	}
	return fmt.Errorf("couldn't find message '%s' in log", message)
}

func verifyLogStream(reader io.Reader) error {
	scan := scanner.NewScanner(reader)
	for scan.Scan() {
		if line := scan.Bytes(); len(line) > 0 {
			lineObject := &log.Line{}
			if err := json.Unmarshal(line, lineObject); err != nil {
				return fmt.Errorf("error reading line %s: %w", string(line), err)
			}
		}
	}
	return nil
}

func setupWorkspace(testdataPath, initialDir string, f *framework.Framework) (string, error) {
	tempDir, err := framework.CopyToTempDir(testdataPath)
	if err != nil {
		return "", err
	}
	ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
	ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)
	return tempDir, nil
}

func setupDockerProvider(binDir, dockerPath string) (*framework.Framework, error) {
	return framework.SetupDockerProvider(binDir, dockerPath)
}

func setupWorkspaceAndUp(
	ctx context.Context,
	testdataPath, initialDir string,
	f *framework.Framework,
	args ...string,
) (string, error) {
	tempDir, err := setupWorkspace(testdataPath, initialDir, f)
	if err != nil {
		return "", err
	}
	return tempDir, f.DevsyUp(ctx, append([]string{tempDir}, args...)...)
}
