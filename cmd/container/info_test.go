package container

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/require"
)

const (
	infoWorkspaceID      = "test-workspace"
	infoIncompleteResult = "incomplete or failed"
	infoSetupFixture     = `{
 "MergedConfig":{"remoteUser":"configured-user","containerUser":"other-user","remoteEnv":{"TOKEN":"do-not-output"}},
 "ContainerDetails":{"Config":{"User":"image-user"}}
 }`
)

func containerInfoFixture(t *testing.T, result string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("container commands require sh")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "setup-result.json")
	require.NoError(t, os.WriteFile(fixture, []byte(result), 0o600))
	script := "#!/bin/sh\nexec /bin/cat " + shellescape.Quote(fixture) + "\n"
	file, err := os.CreateTemp(dir, "sh-*")
	require.NoError(t, err)
	_, err = file.WriteString(script)
	require.NoError(t, err)
	require.NoError(t, file.Chmod(0o700))
	require.NoError(t, file.Close())
	require.NoError(t, os.Symlink(file.Name(), filepath.Join(dir, "sh")))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(config.EnvWorkspaceID, infoWorkspaceID)
	t.Setenv("DEVSY_UI", "")
	t.Setenv("LC_ALL", "C")
	t.Setenv("USER", "wrong-os-user")
	t.Setenv("LOGNAME", "wrong-os-user")
	return fixture
}

func executeContainerInfo(t *testing.T, format string) (string, error) {
	t.Helper()
	command := newInfoCmd(&flags.GlobalFlags{ResultFormat: format})
	command.SetContext(t.Context())
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&out)
	err := command.RunE(command, nil)
	return out.String(), err
}

func TestContainerInfo(t *testing.T) {
	containerInfoFixture(t, infoSetupFixture)
	raw, err := executeContainerInfo(t, "json")
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &got))
	require.Equal(t, map[string]any{
		"sshCommand": "ssh configured-user@test-workspace.devsy",
		"sshHost":    "test-workspace.devsy", "remoteUser": "configured-user",
	}, got)
	text, err := executeContainerInfo(t, "plain")
	require.NoError(t, err)
	for _, value := range []string{
		"Field", "Value", "SSH command", "SSH host", "Remote user",
		"ssh configured-user@test-workspace.devsy",
	} {
		require.Contains(t, text, value)
	}
	for _, omitted := range []string{"do-not-output", "wrong-os-user"} {
		require.NotContains(t, text, omitted)
	}
}

func TestContainerInfoSetupFailures(t *testing.T) {
	for _, tt := range []struct{ name, result, workspaceID, want string }{
		{"missing workspace", infoSetupFixture, "", config.EnvWorkspaceID},
		{"invalid JSON", "{", infoWorkspaceID, "parse container setup result"},
		{"null result", "null", infoWorkspaceID, infoIncompleteResult},
		{"missing config", `{"ContainerDetails":{}}`, infoWorkspaceID, infoIncompleteResult},
		{"missing container", `{"MergedConfig":{}}`, infoWorkspaceID, infoIncompleteResult},
		{
			"failed setup", `{"MergedConfig":{},"ContainerDetails":{},"Error":"private-error"}`,
			infoWorkspaceID, infoIncompleteResult,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			containerInfoFixture(t, tt.result)
			t.Setenv(config.EnvWorkspaceID, tt.workspaceID)
			raw, err := executeContainerInfo(t, "json")
			require.ErrorContains(t, err, tt.want)
			require.Empty(t, raw)
		})
	}
}

func TestContainerInfoReadFailure(t *testing.T) {
	fixture := containerInfoFixture(t, infoSetupFixture)
	require.NoError(t, os.Remove(fixture))
	raw, err := executeContainerInfo(t, "json")
	require.ErrorContains(t, err, "read selected container setup result")
	require.ErrorContains(t, err, "No such file or directory")
	require.Equal(t, clierr.Code("container_info_failed"), clierr.Classify(err).Code)
	require.Empty(t, raw)
}
