package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/pkg/client"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
)

const (
	flagResultFormat = "--result-format"
	formatJSON       = "json"
	cmdList          = "list"
	cmdGet           = "get"
	cmdProvider      = "provider"
)

func (f *Framework) FindWorkspace(ctx context.Context, id string) (*provider2.Workspace, error) {
	list, err := f.DevsyListParsed(ctx)
	if err != nil {
		return nil, err
	}

	workspaceID := workspace.ToID(id)
	for _, w := range list {
		if w.ID == workspaceID {
			return w, nil
		}
	}

	return nil, fmt.Errorf("couldn't find workspace %s", workspaceID)
}

func (f *Framework) DevsyListParsed(ctx context.Context) ([]*provider2.Workspace, error) {
	raw, err := f.DevsyList(ctx)
	if err != nil {
		return nil, err
	}

	retList := []*provider2.Workspace{}
	err = json.Unmarshal([]byte(raw), &retList)
	if err != nil {
		return nil, err
	}

	return retList, nil
}

// DevsyList executes the `devsy list` command in the test framework.
func (f *Framework) DevsyList(ctx context.Context) (string, error) {
	listArgs := []string{cmdList, flagResultFormat, formatJSON}

	out, _, err := f.ExecCommandCapture(ctx, listArgs)
	if err != nil {
		return "", fmt.Errorf("devsy list failed: %s", err.Error())
	}
	return out, nil
}

func (f *Framework) DevsyUpStreams(
	ctx context.Context,
	workspace string,
	additionalArgs ...string,
) (string, string, error) {
	upArgs := []string{"up", "--ide", "none", workspace}
	upArgs = append(upArgs, additionalArgs...)

	stdout, stderr, err := execWithDockerRetry(
		ctx,
		func(ctx context.Context) (string, string, error) {
			return f.ExecCommandCapture(ctx, upArgs)
		},
	)
	if err != nil {
		return stdout, stderr, fmt.Errorf("devsy up failed: %s", err.Error())
	}

	return stdout, stderr, nil
}

// DevsyUpStreamsRaw executes the `devsy up` command capturing stdout/stderr without
// injecting a default --ide flag, allowing callers to supply their own IDE selection.
func (f *Framework) DevsyUpStreamsRaw(
	ctx context.Context,
	workspace string,
	additionalArgs ...string,
) (string, string, error) {
	upArgs := []string{"up", workspace}
	upArgs = append(upArgs, additionalArgs...)

	stdout, stderr, err := execWithDockerRetry(
		ctx,
		func(ctx context.Context) (string, string, error) {
			return f.ExecCommandCapture(ctx, upArgs)
		},
	)
	if err != nil {
		return stdout, stderr, fmt.Errorf("devsy up failed: %s", err.Error())
	}

	return stdout, stderr, nil
}

// DevsyUp executes the `devsy up` command in the test framework.
func (f *Framework) DevsyUpWithIDE(ctx context.Context, additionalArgs ...string) error {
	upArgs := []string{"up", "--debug"}
	upArgs = append(upArgs, additionalArgs...)

	_, _, err := execWithDockerRetry(ctx, func(ctx context.Context) (string, string, error) {
		return f.ExecCommandCapture(ctx, upArgs)
	})
	if err != nil {
		return fmt.Errorf("devsy up failed: %s", err.Error())
	}
	return nil
}

func (f *Framework) DevsyBuild(ctx context.Context, additionalArgs ...string) error {
	upArgs := []string{"build", "--debug"}
	upArgs = append(upArgs, additionalArgs...)

	_, _, err := f.ExecCommandCapture(ctx, upArgs)
	if err != nil {
		return fmt.Errorf("devsy build failed: %s", err.Error())
	}
	return nil
}

func (f *Framework) DevsyUp(ctx context.Context, additionalArgs ...string) error {
	upArgs := []string{"up", "--debug", "--ide", "none"}
	upArgs = append(upArgs, additionalArgs...)

	_, _, err := execWithDockerRetry(ctx, func(ctx context.Context) (string, string, error) {
		return f.ExecCommandCapture(ctx, upArgs)
	})
	if err != nil {
		return fmt.Errorf("devsy up failed: %s", err.Error())
	}
	return nil
}

func (f *Framework) DevsyUpRecreate(ctx context.Context, additionalArgs ...string) error {
	upArgs := []string{"up", "--recreate", "--debug", "--ide", "none"}
	upArgs = append(upArgs, additionalArgs...)

	_, _, err := execWithDockerRetry(ctx, func(ctx context.Context) (string, string, error) {
		return f.ExecCommandCapture(ctx, upArgs)
	})
	if err != nil {
		return fmt.Errorf("devsy up --recreate failed: %s", err.Error())
	}
	return nil
}

func (f *Framework) DevsyUpReset(ctx context.Context, additionalArgs ...string) error {
	upArgs := []string{"up", "--reset", "--debug", "--ide", "none"}
	upArgs = append(upArgs, additionalArgs...)

	_, _, err := execWithDockerRetry(ctx, func(ctx context.Context) (string, string, error) {
		return f.ExecCommandCapture(ctx, upArgs)
	})
	if err != nil {
		return fmt.Errorf("devsy up --reset failed: %s", err.Error())
	}
	return nil
}

func (f *Framework) DevsySSH(
	ctx context.Context,
	workspace string,
	command string,
) (string, error) {
	out, err := execWithSSHRetry(ctx, workspace, func(ctx context.Context) (string, string, error) {
		return f.ExecCommandCapture(ctx, []string{"ssh", workspace, "--command", command})
	})
	if err != nil {
		return "", fmt.Errorf("devsy ssh failed: %s", err.Error())
	}
	return out, nil
}

func (f *Framework) DevsySSHEchoTestString(ctx context.Context, workspace string) error {
	err := f.ExecCommand(
		ctx,
		true,
		true,
		"mYtEsTsTrInG",
		[]string{"ssh", "--command", "echo 'bVl0RXNUc1RySW5H' | base64 -d", workspace},
	)
	if err != nil {
		return fmt.Errorf("devsy ssh failed: %s", err.Error())
	}
	return nil
}

func (f *Framework) DevsyProviderOptionsCheckNamespaceDescription(
	ctx context.Context,
	provider, searchStr string,
) error {
	err := f.ExecCommand(ctx, true, true, searchStr, []string{cmdProvider, cmdGet, provider})
	if err != nil {
		return fmt.Errorf(
			"did not found value %s in devsy provider options output. error: %s",
			searchStr,
			err.Error(),
		)
	}
	return nil
}

func (f *Framework) DevsyProviderList(ctx context.Context, extraArgs ...string) error {
	baseArgs := []string{cmdProvider, cmdList}
	err := f.ExecCommand(ctx, false, true, "", append(baseArgs, extraArgs...))
	if err != nil {
		return fmt.Errorf("devsy provider list failed: %s", err.Error())
	}
	return nil
}

func (f *Framework) DevsyProviderUse(
	ctx context.Context,
	provider string,
	extraArgs ...string,
) error {
	baseArgs := []string{cmdProvider, "configure", provider}
	err := f.ExecCommand(ctx, false, true, "", append(baseArgs, extraArgs...))
	if err != nil {
		return fmt.Errorf("devsy provider configure failed: %s", err.Error())
	}
	return nil
}

func (f *Framework) DevsyStatus(
	ctx context.Context,
	extraArgs ...string,
) (client.WorkspaceStatus, error) {
	baseArgs := []string{"status", flagResultFormat, formatJSON}
	baseArgs = append(baseArgs, extraArgs...)
	stdout, err := f.ExecCommandOutput(ctx, baseArgs)
	if err != nil {
		return client.WorkspaceStatus{}, fmt.Errorf("devsy status failed: %s", err.Error())
	}

	status := &client.WorkspaceStatus{}
	err = json.Unmarshal([]byte(stdout), status)
	if err != nil {
		return client.WorkspaceStatus{}, err
	}

	return *status, nil
}

func (f *Framework) DevsyStop(ctx context.Context, workspace string) error {
	baseArgs := []string{"stop"}
	baseArgs = append(baseArgs, workspace)
	err := f.ExecCommand(ctx, false, false, "", baseArgs)
	if err != nil {
		return fmt.Errorf("devsy stop failed: %s", err.Error())
	}
	return nil
}

func (f *Framework) DevsyDown(ctx context.Context, workspace string) error {
	baseArgs := []string{"down", workspace}
	err := f.ExecCommand(ctx, false, false, "", baseArgs)
	if err != nil {
		return fmt.Errorf("devsy down failed: %s", err.Error())
	}
	return nil
}

func (f *Framework) DevsyProviderAdd(ctx context.Context, args ...string) error {
	baseArgs := []string{cmdProvider, "add"}
	baseArgs = append(baseArgs, args...)
	_, stderr, err := f.ExecCommandCapture(ctx, baseArgs)
	if err != nil {
		// Skip "already exists" errors to make this idempotent
		// This occurs when another test begins before ginkgo.DeferCleanup
		// is called to delete the workspace. The workspace is linked to the
		// provider and the provider cannot be deleted until the workspace is deleted.
		if !strings.Contains(stderr, "already exists") {
			return fmt.Errorf("devsy provider add failed: %s", stderr)
		}
	}
	return nil
}

func (f *Framework) DevsyProviderDelete(ctx context.Context, args ...string) error {
	baseArgs := []string{cmdProvider, "remove"}
	baseArgs = append(baseArgs, args...)
	err := f.ExecCommand(ctx, false, false, "", baseArgs)
	if err != nil {
		return err
	}

	return nil
}

// DevsyProviderRename executes the `devsy provider rename` command in the test framework.
func (f *Framework) DevsyProviderRename(
	ctx context.Context,
	oldName, newName string,
	args ...string,
) error {
	baseArgs := []string{cmdProvider, "rename", oldName, newName}
	baseArgs = append(baseArgs, args...)
	err := f.ExecCommand(ctx, false, false, "", baseArgs)
	if err != nil {
		return fmt.Errorf("devsy provider rename failed: %s", err.Error())
	}

	return nil
}

// DevsyRename executes the `devsy rename` command in the test framework.
func (f *Framework) DevsyRename(
	ctx context.Context,
	oldName, newName string,
	args ...string,
) error {
	baseArgs := []string{"rename", oldName, newName}
	baseArgs = append(baseArgs, args...)
	err := f.ExecCommand(ctx, false, false, "", baseArgs)
	if err != nil {
		return fmt.Errorf("devsy rename failed: %s", err.Error())
	}

	return nil
}

// DevsyProviderOptionsJSON executes `devsy provider get --output json` and returns the raw JSON.
func (f *Framework) DevsyProviderOptionsJSON(
	ctx context.Context,
	providerName string,
) (string, error) {
	args := []string{cmdProvider, cmdGet, providerName, flagResultFormat, formatJSON}
	stdout, _, err := f.ExecCommandCapture(ctx, args)
	if err != nil {
		return "", fmt.Errorf("devsy provider options failed: %s", err.Error())
	}
	return stdout, nil
}

func (f *Framework) DevsyProviderUpdate(ctx context.Context, args ...string) error {
	baseArgs := []string{cmdProvider, "update"}
	baseArgs = append(baseArgs, args...)
	err := f.ExecCommand(ctx, false, false, "", baseArgs)
	if err != nil {
		return fmt.Errorf("devsy provider update failed: %s", err.Error())
	}
	return nil
}

func (f *Framework) DevsyMachineCreate(ctx context.Context, args []string) error {
	baseArgs := []string{"machine", "create"}
	baseArgs = append(baseArgs, args...)
	err := f.ExecCommand(ctx, false, false, "", baseArgs)
	if err != nil {
		return fmt.Errorf("devsy machine create failed: %s", err.Error())
	}
	return nil
}

func (f *Framework) DevsyMachineDelete(ctx context.Context, args []string) error {
	baseArgs := []string{"machine", "delete"}
	baseArgs = append(baseArgs, args...)
	err := f.ExecCommand(ctx, false, false, "", baseArgs)
	if err != nil {
		return fmt.Errorf("devsy machine delete failed: %s", err.Error())
	}
	return nil
}

func (f *Framework) DevsyWorkspaceStop(ctx context.Context, extraArgs ...string) error {
	baseArgs := []string{"stop"}
	baseArgs = append(baseArgs, extraArgs...)
	return f.ExecCommandStdout(ctx, baseArgs)
}

func (f *Framework) DevsyWorkspaceDelete(
	ctx context.Context,
	workspace string,
	extraArgs ...string,
) error {
	baseArgs := []string{"delete", workspace, "--ignore-not-found"}
	baseArgs = append(baseArgs, extraArgs...)

	return f.ExecCommand(ctx, false, true, fmt.Sprintf("deleted workspace %s", workspace), baseArgs)
}

func (f *Framework) SetupGPG(tmpDir string) error {
	if _, err := exec.LookPath("gpg"); err != nil {
		if installErr := exec.Command("sudo", "apt-get", "install", "gnupg2", "-y").
			Run(); installErr != nil {
			return fmt.Errorf("gpg not found and failed to install gnupg2: %w", installErr)
		}
	}

	// #nosec G204 -- gpg with fixed arguments for test GPG key setup
	if err := exec.Command("gpg", "--import", filepath.Join(tmpDir, "gpg-public.key")).
		Run(); err != nil {
		return fmt.Errorf("failed to import gpg public key: %w", err)
	}

	// #nosec G204 -- gpg with fixed arguments for test GPG key setup
	if err := exec.Command("gpg", "--import", filepath.Join(tmpDir, "gpg-private.key")).
		Run(); err != nil {
		return fmt.Errorf("failed to import gpg private key: %w", err)
	}

	if err := exec.Command("gpgconf", "--kill", "gpg-agent").Run(); err != nil {
		return fmt.Errorf("failed to kill gpg-agent: %w", err)
	}

	if err := exec.Command("gpg-agent", "--homedir", "$HOME/.gnupg", "--use-standard-socket", "--daemon").
		Run(); err != nil {
		return fmt.Errorf("failed to start gpg-agent: %w", err)
	}

	return exec.Command("gpg", "-k").Run()
}

func (f *Framework) DevsySSHGpgTestKey(ctx context.Context, workspace string) error {
	pubKeyB, err := exec.Command("sh", "-c", "gpg -k --with-colons 2>/dev/null | grep sec | base64 -w0").
		Output()
	if err != nil {
		return err
	}

	// First run to trigger the first forwarding
	stdout, _, err := f.ExecCommandCapture(ctx, []string{
		"ssh",
		"--agent-forwarding",
		"--gpg-agent-forwarding",
		"--command",
		"gpg -k --with-colons 2>/dev/null |grep sec |  base64 -w0", workspace,
	})
	if err != nil {
		return err
	}

	if stdout != string(pubKeyB) {
		return fmt.Errorf(
			"devsy gpg public key forwarding failed, expected %s, got %s",
			string(pubKeyB),
			stdout,
		)
	}

	return nil
}

func (f *Framework) DevsyPortTest(ctx context.Context, port string, workspace string) error {
	_, err := execWithSSHRetry(ctx, workspace, func(ctx context.Context) (string, string, error) {
		return f.ExecCommandCapture(ctx, []string{"ssh", "--forward-ports", port, workspace})
	})
	return err
}

func (f *Framework) DevsyProviderFindOption(
	ctx context.Context,
	provider string,
	searchStr string,
	extraArgs ...string,
) error {
	baseArgs := []string{cmdProvider, cmdGet, provider}
	err := f.ExecCommand(ctx, false, true, searchStr, append(baseArgs, extraArgs...))
	if err != nil {
		return fmt.Errorf("devsy provider use failed: %s", err.Error())
	}
	return nil
}

func (f *Framework) DevsyContextCreate(
	ctx context.Context,
	name string,
	extraArgs ...string,
) error {
	baseArgs := []string{"context", "create", name}
	err := f.ExecCommand(ctx, false, true, "", append(baseArgs, extraArgs...))
	if err != nil {
		return fmt.Errorf("devsy context create failed: %s", err.Error())
	}
	return nil
}

func (f *Framework) DevsyContextUse(ctx context.Context, name string, extraArgs ...string) error {
	baseArgs := []string{"context", "use", name}
	err := f.ExecCommand(ctx, false, true, "", append(baseArgs, extraArgs...))
	if err != nil {
		return fmt.Errorf("devsy context use failed: %s", err.Error())
	}
	return nil
}

func (f *Framework) DevsyContextDelete(
	ctx context.Context,
	name string,
	extraArgs ...string,
) error {
	baseArgs := []string{"context", "delete", name}
	err := f.ExecCommand(ctx, false, true, "", append(baseArgs, extraArgs...))
	if err != nil {
		return fmt.Errorf("devsy context delete failed: %s", err.Error())
	}
	return nil
}

func (f *Framework) DevsyIDEUse(ctx context.Context, ide string, extraArgs ...string) error {
	baseArgs := []string{"ide", "use", ide}
	err := f.ExecCommand(ctx, false, true, "", append(baseArgs, extraArgs...))
	if err != nil {
		return fmt.Errorf("devsy ide use failed %s", err.Error())
	}
	return nil
}

func (f *Framework) DevsyLogs(ctx context.Context, workspace string) (string, error) {
	args := []string{"logs", workspace}
	stdout, _, err := f.ExecCommandCapture(ctx, args)
	if err != nil {
		return "", fmt.Errorf("devsy logs failed: %s", err.Error())
	}
	return stdout, nil
}

func (f *Framework) DevsyIDEList(ctx context.Context, extraArgs ...string) (string, error) {
	baseArgs := []string{"ide", cmdList}
	return f.ExecCommandOutput(ctx, append(baseArgs, extraArgs...))
}

// SetupDockerProvider creates a new framework, removes any existing docker provider,
// adds a fresh one with the given docker path, and sets it as the active provider.
func SetupDockerProvider(binDir, dockerPath string) (*Framework, error) {
	f := NewDefaultFramework(binDir)
	_ = f.DevsyProviderDelete(context.Background(), "docker")
	if err := f.DevsyProviderAdd(
		context.Background(),
		"docker",
		"-o",
		"DOCKER_PATH="+dockerPath,
	); err != nil {
		return nil, fmt.Errorf("failed to add docker provider: %w", err)
	}
	return f, f.DevsyProviderUse(context.Background(), "docker")
}
