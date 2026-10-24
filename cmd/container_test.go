package cmd

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/require"
)

const (
	containerCommandName = "container"
	infoCommandName      = "info"
)

func TestContainerInfoFailureRendering(t *testing.T) {
	t.Setenv(config.EnvWorkspaceID, "")
	root, _ := BuildRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{
		containerCommandName, infoCommandName, "--home", t.TempDir(),
		"--context", "not-configured", "--provider", "not-configured", "--result-format", "json",
	})
	err := root.Execute()
	require.ErrorContains(t, err, config.EnvWorkspaceID)
	require.Empty(t, out.String())
	for _, machine := range []bool{false, true} {
		reader, writer, pipeErr := os.Pipe()
		require.NoError(t, pipeErr)
		original := os.Stderr
		os.Stderr = writer
		code := exitCodeForError(err, machine)
		os.Stderr = original
		require.NoError(t, writer.Close())
		raw, readErr := io.ReadAll(reader)
		require.NoError(t, readErr)
		require.NoError(t, reader.Close())
		require.Equal(t, 1, code)
		require.Contains(t, string(raw), config.EnvWorkspaceID)
		require.Contains(t, string(raw), "container_info_failed")
	}
}

func TestContainerInfoRunsRootValidation(t *testing.T) {
	t.Setenv(config.EnvWorkspaceID, "")
	root, _ := BuildRoot()
	root.SetArgs([]string{
		containerCommandName, infoCommandName, "--log-level", "definitely-invalid",
	})
	err := root.Execute()
	require.ErrorContains(t, err, "invalid log level")
}
