//go:build !windows

package ghcredentials

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBackendDiscoveryToleratesLoginShellOutput(t *testing.T) {
	const availableResult = `{"status":"available"}`
	const missingResult = `{"status":"backend_missing"}`
	for _, tc := range []struct {
		name, profile, output, exit, want string
		invalid                           bool
	}{
		{name: "plain response", output: availableResult, want: stateAvailable},
		{name: "login banner", profile: "Welcome to the workspace\n", output: availableResult, want: stateAvailable},
		{
			name: "banner without newline", profile: "Activating user environment",
			output: availableResult, want: stateAvailable,
		},
		{
			name: "backend missing", profile: "User profile loaded\n",
			output: missingResult + "\n\n", want: "backend_missing",
		},
		{name: "JSON in profile", profile: availableResult + "\n", output: missingResult, want: "backend_missing"},
		{name: "malformed response", profile: "Welcome\n", output: "not JSON", invalid: true},
		{name: "empty response", profile: "Welcome\n", invalid: true},
		{
			name: "malformed response after JSON profile", profile: availableResult + "\n",
			output: "invalid", invalid: true,
		},
		{name: "failed command", output: availableResult, exit: "17", invalid: true},
		{
			name: "oversized banner", profile: strings.Repeat("x", maxTokenOutput+1),
			output: availableResult, invalid: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary, record := loginShellFixture(t)
			t.Setenv("DEVSY_TEST_PROFILE_OUTPUT", tc.profile)
			t.Setenv("DEVSY_TEST_BACKEND_OUTPUT", tc.output)
			t.Setenv("DEVSY_TEST_BACKEND_EXIT", tc.exit)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := discoverWithLoginShell(ctx, binary, "guest-user")
			if tc.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, result.Status)
			}
			args, err := readHelperFile(record)
			require.NoError(t, err)
			require.True(t, strings.HasPrefix(string(args), "-\nguest-user\n-c\n"))
		})
	}
}

func loginShellFixture(t *testing.T) (binary, record string) {
	t.Helper()
	dir := t.TempDir()
	record = filepath.Join(dir, "su-args")
	writeTestExecutable(t, filepath.Join(dir, "su"), []byte(`#!/bin/sh
printf '%s\n' "$@" > "$DEVSY_TEST_SU_ARGS"
printf '%s' "$DEVSY_TEST_PROFILE_OUTPUT"
exec /bin/sh -c "$4"
`))
	binary = filepath.Join(dir, "agent's binary")
	writeTestExecutable(t, binary, []byte(`#!/bin/sh
test "$#" = 2 && test "$1" = internal && test "$2" = gh-backend || exit 42
printf '%s\n' "$DEVSY_TEST_BACKEND_OUTPUT"
exit "${DEVSY_TEST_BACKEND_EXIT:-0}"
`))
	t.Setenv("PATH", dir)
	t.Setenv("DEVSY_TEST_SU_ARGS", record)
	return binary, record
}

func TestBackendDiscoveryLoginShellCancellationIsBounded(t *testing.T) {
	dir := t.TempDir()
	writeTestExecutable(t, filepath.Join(dir, "su"), []byte("#!/bin/sh\nexec /bin/sleep 10\n"))
	t.Setenv("PATH", dir)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := discoverWithLoginShell(ctx, "/unused-agent", "guest-user")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), time.Second)
}

func TestInstallWithSudoPreservesArgumentsAndFailure(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	writeTestExecutable(t, filepath.Join(dir, "sudo"), []byte(
		"#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DEVSY_TEST_SUDO_ARGS\"\n"+
			"exit \"$DEVSY_TEST_SUDO_EXIT\"\n",
	))
	t.Setenv("PATH", dir)
	t.Setenv("DEVSY_TEST_SUDO_ARGS", record)
	t.Setenv("DEVSY_TEST_SUDO_EXIT", "0")
	require.NoError(t, installWithSudo(context.Background(), "/absolute path/devsy", 12049))
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer func() { _ = root.Close() }()
	args, err := root.ReadFile("args")
	require.NoError(t, err)
	require.Equal(
		t,
		"-n\n/absolute path/devsy\ninternal\ninstall-gh-wrapper\n12049\n",
		string(args),
	)
	t.Setenv("DEVSY_TEST_SUDO_EXIT", "1")
	var exit *exec.ExitError
	require.ErrorAs(t, installWithSudo(context.Background(), "/absolute path/devsy", 12049), &exit)
	require.Equal(t, 1, exit.ExitCode())
}

func TestBackendDiscoveryPreservesMissingCLIBackupsAndUserShims(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gh")
	t.Setenv("PATH", dir)
	_, err := discoverBackend(path)
	require.ErrorIs(t, err, exec.ErrNotFound)
	require.NoFileExists(t, path)
	writeTestExecutable(t, path+backupSuffix, []byte("#!/bin/sh\nprintf old"))
	backend, err := discoverBackend(path)
	require.NoError(t, err)
	require.Equal(t, path+backupSuffix, backend)
	writeTestExecutable(t, path, []byte("#!/bin/sh\nprintf fresh"))
	backend, err = discoverBackend(path)
	require.NoError(t, err)
	require.Equal(t, path, backend)
	//nolint:gosec // Execute only the dummy backend just written into this test-owned directory.
	out, err := exec.Command(backend).Output()
	require.NoError(t, err)
	require.Equal(t, "fresh", string(out))
	marker := filepath.Join(dir, "shim-ran")
	writeTestExecutable(t, path, []byte("#!/bin/sh\ntouch '"+marker+"'\nprintf shim"))
	_, err = discoverBackend(path)
	require.NoError(t, err)
	require.NoFileExists(t, marker, "discovery ran a user shim")
}

func TestInstallWithSudoCancellationDoesNotWaitForPasswordOrProcess(t *testing.T) {
	dir := t.TempDir()
	writeTestExecutable(t, filepath.Join(dir, "sudo"), []byte("#!/bin/sh\nexec /bin/sleep 10\n"))
	t.Setenv("PATH", dir)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	require.ErrorIs(t, installWithSudo(ctx, "/unused-agent", 12049), context.DeadlineExceeded)
	require.Less(t, time.Since(started), time.Second)
}

const (
	testOriginalName  = "real-gh"
	testExitScript    = "#!/bin/sh\nexit 0"
	testRegularFile   = "file"
	testRelativeLink  = "relative symlink"
	testInstallBinary = "/devsy"
)

func writeTestExecutable(t *testing.T, path string, data []byte) {
	t.Helper()
	dir, err := os.OpenRoot(filepath.Dir(path))
	require.NoError(t, err)
	defer func() { _ = dir.Close() }()
	require.NoError(t, dir.WriteFile(filepath.Base(path), data, 0o700))
}

func TestInstallWrapperPreservesOriginalAndUpdates(t *testing.T) {
	for _, kind := range []string{testRegularFile, "absolute symlink", testRelativeLink} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "gh")
			target := filepath.Join(dir, testOriginalName)
			first := []byte("#!/bin/sh\nprintf first")
			writeTestExecutable(t, target, first)
			switch kind {
			case testRegularFile:
				writeTestExecutable(t, path, first)
			case "absolute symlink":
				require.NoError(t, os.Symlink(target, path))
			case testRelativeLink:
				require.NoError(t, os.Symlink(testOriginalName, path))
			}
			require.NoError(t, installWrapper(path, "/devsy first", 12049))
			backup := path + backupSuffix
			original, err := readFile(backup)
			require.NoError(t, err)
			require.Equal(t, first, original)
			if kind != testRegularFile {
				link, err := os.Readlink(backup)
				require.NoError(t, err)
				if kind == testRelativeLink {
					require.Equal(t, testOriginalName, link)
				} else {
					require.Equal(t, target, link)
				}
			}
			require.NoError(t, installWrapper(path, "/devsy second", 12050))
			original, err = readFile(backup)
			require.NoError(t, err)
			require.Equal(t, first, original)
			script, err := readFile(path)
			require.NoError(t, err)
			require.Contains(t, string(script), "12050")
			require.Contains(t, string(script), "'/devsy second'")
			require.NoError(t, os.Remove(path))
			second := []byte("#!/bin/sh\nprintf second")
			writeTestExecutable(t, path, second)
			require.NoError(t, installWrapper(path, testInstallBinary, 12049))
			original, err = readFile(backup)
			require.NoError(t, err)
			require.Equal(t, second, original)
		})
	}
}

func TestInstallWrapperWithoutOriginalAndInterruptedMove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gh")
	require.NoError(t, installWrapper(path, testInstallBinary, 12049))
	_, err := os.Lstat(path + backupSuffix)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, os.Remove(path))
	original := []byte("#!/bin/sh\nprintf preserved")
	writeTestExecutable(t, path+backupSuffix, original)
	require.NoError(t, installWrapper(path, testInstallBinary, 12049))
	got, err := readFile(path + backupSuffix)
	require.NoError(t, err)
	require.Equal(t, original, got)
}

func TestInstallWrapperConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gh")
	original := []byte("#!/bin/sh\nprintf original")
	writeTestExecutable(t, path, original)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() { errs <- installWrapper(path, testInstallBinary, 12049) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	got, err := readFile(path + backupSuffix)
	require.NoError(t, err)
	require.Equal(t, original, got)
}

func TestInstallWrapperFailurePreservesOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gh")
	original := []byte("#!/bin/sh\nprintf original")
	writeTestExecutable(t, path, original)
	require.NoError(t, os.Mkdir(path+backupSuffix, 0o700))
	require.Error(t, installWrapper(path, testInstallBinary, 12049))
	got, err := readFile(path)
	require.NoError(t, err)
	require.Equal(t, original, got)
	entries, err := filepath.Glob(filepath.Join(dir, ".devsy-gh-wrapper-*"))
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestRealBinaryRespectsPATHAndBackup(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	early := filepath.Join(root, "early")
	standard := filepath.Join(root, "standard")
	late := filepath.Join(root, "late")
	for _, dir := range []string{early, standard, late} {
		require.NoError(t, os.Mkdir(dir, 0o700))
	}
	wrapper := filepath.Join(standard, "gh")
	require.NoError(t, installWrapper(wrapper, testInstallBinary, 12049))
	later := filepath.Join(late, "gh")
	writeTestExecutable(t, later, []byte(testExitScript))
	t.Setenv("PATH", standard+string(os.PathListSeparator)+late)
	got, err := realBinary(wrapper)
	require.NoError(t, err)
	require.Equal(t, later, got)
	backup := wrapper + backupSuffix
	writeTestExecutable(t, backup, []byte(testExitScript))
	got, err = realBinary(wrapper)
	require.NoError(t, err)
	require.Equal(t, backup, got)
	selected := filepath.Join(early, "gh")
	writeTestExecutable(t, selected, []byte(testExitScript))
	t.Setenv("PATH", early+string(os.PathListSeparator)+standard+string(os.PathListSeparator)+late)
	got, err = realBinary(wrapper)
	require.NoError(t, err)
	require.Equal(t, selected, got)
	t.Setenv("PATH", late)
	got, err = realBinary(wrapper)
	require.NoError(t, err)
	require.Equal(t, later, got)
	require.NoError(t, os.Remove(selected))
	require.NoError(t, os.Remove(backup))
	require.NoError(t, os.Link(wrapper, backup))
	got, err = realBinary(wrapper)
	require.NoError(t, err)
	require.Equal(t, later, got)
}

func TestRealBinaryUsesBackupOutsidePATH(t *testing.T) {
	dir := t.TempDir()
	wrapper := filepath.Join(dir, "gh")
	require.NoError(t, installWrapper(wrapper, testInstallBinary, 12049))
	t.Setenv("PATH", t.TempDir())
	_, err := realBinary(wrapper)
	require.ErrorIs(t, err, exec.ErrNotFound)
	backup := wrapper + backupSuffix
	writeTestExecutable(t, backup, []byte(testExitScript))
	got, err := realBinary(wrapper)
	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(backup)
	require.NoError(t, err)
	require.Equal(t, want, got)
	alias := filepath.Join(t.TempDir(), "gh")
	require.NoError(t, os.Symlink(wrapper, alias))
	got, err = realBinary(alias)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.NoError(t, os.Remove(backup))
	require.NoError(t, os.Link(wrapper, backup))
	_, err = realBinary(wrapper)
	require.ErrorIs(t, err, exec.ErrNotFound)
}
