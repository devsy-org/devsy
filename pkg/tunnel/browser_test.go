package tunnel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/exitcode"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWorkspaceClient struct{}

func (fakeWorkspaceClient) Provider() string { return "" }
func (fakeWorkspaceClient) Context() string  { return "" }

func (fakeWorkspaceClient) RefreshOptions(
	ctx context.Context, userOptions []string, reconfigure bool,
) error {
	return nil
}

func (fakeWorkspaceClient) Status(
	ctx context.Context, options client2.StatusOptions,
) (client2.Status, error) {
	return "", nil
}

func (fakeWorkspaceClient) Stop(ctx context.Context, options client2.StopOptions) error {
	return nil
}

func (fakeWorkspaceClient) Delete(ctx context.Context, options client2.DeleteOptions) error {
	return nil
}
func (fakeWorkspaceClient) Workspace() string                    { return "" }
func (fakeWorkspaceClient) WorkspaceConfig() *provider.Workspace { return nil }
func (fakeWorkspaceClient) Lock(ctx context.Context) error       { return nil }
func (fakeWorkspaceClient) Unlock()                              {}

var _ client2.BaseWorkspaceClient = fakeWorkspaceClient{}

// exitError runs a shell command that exits with the given code and returns
// the resulting error (which wraps *exec.ExitError).
func exitError(t *testing.T, code int) error {
	t.Helper()
	// #nosec G204 -- test helper with controlled exit code argument
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	require.Error(t, err)
	return err
}

func baseSSHArgs(ctx, user, ws string) []string {
	return []string{
		"workspace", "ssh", "--user=" + user, "--agent-forwarding=false",
		"--start-services=false", "--context", ctx, ws,
	}
}

func TestBuildSSHCommandArgs(t *testing.T) {
	tests := []struct {
		name      string
		context   string
		workspace string
		user      string
		debug     bool
		extraArgs []string
		expected  []string
	}{
		{
			name: "basic root user", context: "default", workspace: "my-workspace",
			user:     "root",
			expected: baseSSHArgs("default", "root", "my-workspace"),
		},
		{
			name: "non-root workspace user", context: "default", workspace: "my-workspace",
			user:     "vscode",
			expected: baseSSHArgs("default", "vscode", "my-workspace"),
		},
		{
			name: "empty user falls back to root", context: "default", workspace: "my-workspace",
			user:     "",
			expected: baseSSHArgs("default", "root", "my-workspace"),
		},
		{
			name: "with debug", context: "default", workspace: "my-workspace",
			user: "vscode", debug: true,
			expected: append(baseSSHArgs("default", "vscode", "my-workspace"), "--debug"),
		},
		{
			name: "with extra args", context: "prod", workspace: "ws",
			user:      "vscode",
			extraArgs: []string{"--stdio", "--log-output=raw"},
			expected:  append(baseSSHArgs("prod", "vscode", "ws"), "--stdio", "--log-output=raw"),
		},
		{
			name: "with debug and extra args", context: "default", workspace: "my-workspace",
			user: "vscode", debug: true, extraArgs: []string{"--stdio"},
			expected: append(baseSSHArgs("default", "vscode", "my-workspace"), "--debug", "--stdio"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSSHCommandArgs(tt.context, tt.workspace, tt.user, tt.debug, tt.extraArgs)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestBuildBackhaulCmd_UsesResolvedRemoteUser(t *testing.T) {
	writer := &bytes.Buffer{}
	cmd := buildBackhaulCmd(context.Background(), backhaulCmdParams{
		execPath:   "/usr/bin/true",
		remoteUser: "vscode",
		client:     fakeWorkspaceClient{},
		authSockID: "sock123",
		writer:     writer,
	})

	joined := strings.Join(cmd.Args, " ")
	assert.Contains(t, joined, "--user vscode",
		"backhaul connection must use the resolved workspace user, not root, "+
			"so it doesn't fight the primary tunnel's ssh-server/setup-gpg sessions "+
			"over the shared /tmp coordination files")
}

func TestIsTransientBackhaulErr(t *testing.T) {
	transient := exitError(t, exitcode.Retryable)
	otherExit := exitError(t, 1)

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{name: "nil error", err: nil, expected: false},
		{name: "plain error is not an exit error", err: errors.New("boom"), expected: false},
		{name: "exit code WorkspaceNotFound", err: transient, expected: true},
		{
			name:     "wrapped exit code WorkspaceNotFound",
			err:      fmt.Errorf("wrap: %w", transient),
			expected: true,
		},
		{name: "other exit code", err: otherExit, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isTransientBackhaulErr(tt.err))
		})
	}
}
