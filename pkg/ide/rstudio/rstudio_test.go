package rstudio

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	installerFixtureMode = "rstudio-installer-output-fixture"
	installerFoldedValue = "synthetic-rstudio-folded-value"
	installerDetail      = "public RStudio installer detail"
)

func TestInstallerErrorMasksFoldedAuthorization(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	cmd := &exec.Cmd{
		Path: executable,
		Args: []string{
			executable,
			"-test.run=^TestInstallerOutputFixture$",
			"--",
			installerFixtureMode,
		},
	}
	err = runInstallerCommand("fixture installer", cmd)
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, 23, exitErr.ExitCode())
	require.Contains(t, err.Error(), "fixture installer")
	require.Contains(t, err.Error(), installerDetail)
	require.NotContains(t, err.Error(), installerFoldedValue)
}

func TestInstallerOutputFixture(t *testing.T) {
	if os.Args[len(os.Args)-1] != installerFixtureMode {
		return
	}
	_, _ = fmt.Fprint(
		os.Stderr,
		"Authorization:\n Bearer "+installerFoldedValue+"\n\n"+installerDetail,
	)
	os.Exit(23)
}
