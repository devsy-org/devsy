package up

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	docker "github.com/devsy-org/devsy/pkg/docker"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/scanner"
	"github.com/onsi/ginkgo/v2"
)

// logLine represents a single JSON log line from devsy output.
// Only the fields we need for test assertions are included.
type logLine struct {
	Message string `json:"message,omitempty"`
	Msg     string `json:"msg,omitempty"`
}

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
		[]string{"workspace", "ssh", "--command", command, projectName},
	)
	return strings.TrimSpace(output), err
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
			lineObject := &logLine{}
			if err := json.Unmarshal(line, lineObject); err == nil {
				msg := lineObject.Message
				if msg == "" {
					msg = lineObject.Msg
				}
				if strings.Contains(msg, message) {
					return nil
				}
				// Agent JSON may be embedded in the parent's error chain.
				// Parse any nested JSON lines within the msg to resolve
				// double-escaped quotes.
				for part := range strings.SplitSeq(msg, "\n") {
					part = strings.TrimSpace(part)
					if len(part) > 0 && part[0] == '{' {
						inner := &logLine{}
						if json.Unmarshal([]byte(part), inner) == nil {
							innerMsg := inner.Message
							if innerMsg == "" {
								innerMsg = inner.Msg
							}
							if strings.Contains(innerMsg, message) {
								return nil
							}
						}
					}
				}
			}
		}
	}
	return fmt.Errorf("couldn't find message %q in log", message)
}

func verifyLogStream(reader io.Reader) error {
	scan := scanner.NewScanner(reader)
	for scan.Scan() {
		if line := scan.Bytes(); len(line) > 0 {
			lineObject := &logLine{}
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
