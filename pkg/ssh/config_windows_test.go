//go:build windows

package ssh

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddHostSectionNormalizesWindowsExecPath(t *testing.T) {
	execPath := `C:\Users\Test User\AppData\Local\Programs\Devsy\resources\bin\devsy.exe`
	result, err := addHostSection("", execPath, addHostParams{
		host:      "testhost",
		user:      "ubuntu",
		context:   "default",
		workspace: "testworkspace",
		workdir:   "/workspaces/project",
	})
	require.NoError(t, err)
	require.Contains(
		t,
		result,
		`ProxyCommand "C:/Users/Test User/AppData/Local/Programs/Devsy/resources/bin/devsy.exe" workspace ssh --stdio --context default --user ubuntu testworkspace`,
	)
	require.NotContains(t, result, `C:\Users\Test User`)
	require.Contains(t, result, `--workdir "/workspaces/project"`)
}
