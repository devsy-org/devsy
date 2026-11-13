package dependencies_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/devsy-org/devsy/pkg/dependencies"
	"github.com/stretchr/testify/require"
)

// These tests exercise the native Windows filesystem only when run on Windows.
func TestLookupWindowsPATHAndExtensions(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.Mkdir(bin, 0o700))
	for _, name := range []string{"Docker.CMD", "docker.EXE", "docker.PS1", "docker.BAT"} {
		body := "@echo EXECUTED > \"" + filepath.Join(root, "EXECUTED") + "\"\r\nexit 99\r\n"
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(body), 0o600))
	}
	dependencytest.Preserve(t, root)
	environment, err := dependencies.NewLookupEnvironment(map[string]string{
		"Path": bin, "PathExt": ".CMD;.EXE;.PS1;.BAT",
	}, root)
	require.NoError(t, err)
	result, err := environment.LookupExecutable(
		context.Background(),
		dependencies.LookupRequest{Command: "DOCKER"},
	)
	require.NoError(t, err)
	require.Len(t, result.Candidates, 4)
	require.True(t, result.Candidates[0].Script)
	require.False(t, result.Candidates[1].Script)
	require.True(t, result.Candidates[2].Script)
	require.True(t, result.Candidates[3].Script)
	require.False(t, result.Executed)
	explicit, err := environment.LookupExecutable(context.Background(), dependencies.LookupRequest{
		Command: filepath.Join("bin", "docker"), Explicit: true,
	})
	require.NoError(t, err)
	require.Empty(t, explicit.ReasonCode)
	require.True(t, explicit.Candidates[len(explicit.Candidates)-1].Script)
}

func TestLookupWindowsEmptyPATHEXTAndAmbiguousEnvironment(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.Mkdir(bin, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "docker.exe"), []byte("fixture"), 0o600))
	environment, err := dependencies.NewLookupEnvironment(
		map[string]string{"PATH": bin, "PATHEXT": ""},
		root,
	)
	require.NoError(t, err)
	result, err := environment.LookupExecutable(
		context.Background(),
		dependencies.LookupRequest{Command: "docker"},
	)
	require.NoError(t, err)
	require.Equal(t, dependencies.LookupMissing, result.ReasonCode)
	result, err = environment.LookupExecutable(context.Background(), dependencies.LookupRequest{
		Command: filepath.Join(bin, "docker.exe"), Explicit: true,
	})
	require.NoError(t, err)
	require.Empty(t, result.ReasonCode)
	_, err = dependencies.NewLookupEnvironment(map[string]string{"Path": bin, "PATH": bin}, root)
	require.Error(t, err)
	for _, path := range []string{`C:docker.exe`, `\docker.exe`, `/docker.exe`, `/mnt/c/docker`, `C:\missing\docker.exe`} {
		result, err = environment.LookupExecutable(
			context.Background(),
			dependencies.LookupRequest{Command: path, Explicit: true},
		)
		require.NoError(t, err)
		require.Equal(t, dependencies.LookupInvalidPath, result.ReasonCode)
	}
}

func TestLookupWindowsExplicitSuffixNeverFallsBack(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "missing.exe")
	require.NoError(t, os.WriteFile(path+".CMD", []byte("exit 99\r\n"), 0o600))
	dependencytest.Preserve(t, root)
	environment, err := dependencies.NewLookupEnvironment(
		map[string]string{"PATHEXT": ".CMD"},
		root,
	)
	require.NoError(t, err)
	result, err := environment.LookupExecutable(
		context.Background(),
		dependencies.LookupRequest{Command: path, Explicit: true},
	)
	require.NoError(t, err)
	require.Equal(t, dependencies.LookupInvalidPath, result.ReasonCode)
	require.Len(t, result.Candidates, 1)
	require.Equal(t, dependencies.FileMissing, result.Candidates[0].State)
}
