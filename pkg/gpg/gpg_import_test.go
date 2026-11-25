package gpg

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	importFixtureMode  = "gpg-import-output-fixture"
	importFixtureInput = "literal GPG import fixture input"
	importFoldedValue  = "synthetic-gpg-import-folded-value"
	importPublicDetail = "public GPG import detail"
)

func TestGpgImportMasksReturnedFailureOutput(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	cmd := &exec.Cmd{
		Path: executable,
		Args: []string{
			executable,
			"-test.run=^TestGpgImportOutputFixture$",
			"--",
			importFixtureMode,
		},
	}
	err = runGpgImport(cmd, []byte(importFixtureInput))
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, 23, exitErr.ExitCode())
	require.Contains(t, err.Error(), importPublicDetail)
	require.NotContains(t, err.Error(), importFoldedValue)
}

func TestGpgImportOutputFixture(t *testing.T) {
	if os.Args[len(os.Args)-1] != importFixtureMode {
		return
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil || string(input) != importFixtureInput {
		os.Exit(1)
	}
	_, _ = fmt.Fprint(
		os.Stderr,
		"Authorization:\n Bearer "+importFoldedValue+"\n\n"+importPublicDetail,
	)
	os.Exit(23)
}
