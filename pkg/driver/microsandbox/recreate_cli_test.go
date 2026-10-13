package microsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	recreateCLIBaseName     = "msb"
	recreateCLIFixtureEnv   = "DEVSY_TEST_MSB_CREATE_FIXTURE"
	recreateCLIFixtureMode  = "capture-argv"
	recreateCLIArgumentFile = "create-argv.json"
)

func TestMain(m *testing.M) {
	if os.Getenv(recreateCLIFixtureEnv) == recreateCLIFixtureMode {
		if handled, err := captureRecreateCLIArguments(); handled {
			if err != nil {
				_, _ = fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

// Only the test executable copied to an owned msb path enters fixture mode.
// Its fixed result file is adjacent to that copy; an environment value cannot
// select an arbitrary output path or divert the ordinary package test binary.
func captureRecreateCLIArguments() (bool, error) {
	executable, err := os.Executable()
	if err != nil {
		return false, err
	}
	if filepath.Base(executable) != recreateCLIExecutableName() {
		return false, nil
	}
	data, err := json.Marshal(os.Args[1:])
	if err != nil {
		return true, err
	}
	path := filepath.Join(filepath.Dir(executable), recreateCLIArgumentFile)
	// #nosec G703 -- Fixed result beside this fixture's private copied test executable.
	return true, os.WriteFile(path, data, 0o600)
}

func TestCLIClientCreateReplacementAuthorizationIsRequestLocal(t *testing.T) {
	fixture := installRecreateCLIExecutable(t)
	argumentPath := filepath.Join(filepath.Dir(fixture), recreateCLIArgumentFile)
	const sandbox = "owned-create-argv"
	spec := sandboxSpec{
		Image:      "registry.invalid/owned/image:literal",
		Entrypoint: "/owned/entry point",
		Cmd: []string{
			"-c", `printf '%s' "$1"; : 'literal --replace $(not-a-command)'`,
			"sh", "two words", "", `quotes'"\雪`,
		},
	}
	client := cliClient{}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	for _, allowed := range []bool{false, true, false} {
		removeRecreateCLIArguments(t, argumentPath)
		spec.AllowRecreate = allowed
		require.NoError(t, client.Create(ctx, sandbox, spec))
		var actual []string
		// #nosec G304 -- Fixed result file beside the owned executable copy.
		data, err := os.ReadFile(argumentPath)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &actual))
		want := []string{"run", "--detach", "--name", sandbox}
		if allowed {
			want = append(want, "--replace")
		}
		want = append(want, "--entrypoint", spec.Entrypoint, spec.Image, "--")
		want = append(want, spec.Cmd...)
		require.Equal(t, want, actual, "actual child argv with allow-recreate=%t", allowed)
		t.Logf("allow-recreate=%t actual child argv=%q", allowed, actual)
	}
}

func recreateCLIExecutableName() string {
	if runtime.GOOS == "windows" {
		return recreateCLIBaseName + ".exe"
	}
	return recreateCLIBaseName
}

func installRecreateCLIExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), recreateCLIExecutableName())
	require.NoError(t, copyRecreateCLIExecutable(path))
	t.Setenv(recreateCLIFixtureEnv, recreateCLIFixtureMode)
	t.Setenv("PATH", filepath.Dir(path)+string(os.PathListSeparator)+os.Getenv("PATH"))
	resolved, err := exec.LookPath(recreateCLIBaseName)
	require.NoError(t, err)
	require.Equal(t, path, resolved, "Create must execute only this owned fixture")
	return path
}

func copyRecreateCLIExecutable(path string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	// #nosec G304 -- Copy only the current test executable, never a supplied binary.
	source, err := os.Open(executable)
	if err != nil {
		return err
	}
	// #nosec G304 G302 -- New executable copy is inside the test's private TempDir.
	target, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return errors.Join(err, source.Close())
	}
	_, copyErr := io.Copy(target, source)
	return errors.Join(copyErr, target.Close(), source.Close())
}

func removeRecreateCLIArguments(t *testing.T, path string) {
	t.Helper()
	err := os.Remove(path)
	if !errors.Is(err, os.ErrNotExist) {
		require.NoError(t, err)
	}
}
