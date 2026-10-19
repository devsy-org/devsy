package dependencies

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Synthetic request-level tests on Windows use the production safety walk with
// injected metadata. No UNC or device path reaches a native filesystem call.
const lookupWindowsTestPATH = "PATH"

type windowsUnsafeLookupCase struct {
	name    string
	cwd     string
	path    string
	request LookupRequest
}

func unsafeWindowsLookupCases(localCWD string) []windowsUnsafeLookupCase {
	const (
		remote  = `\\server\share`
		safeCWD = `C:\safe`
	)
	return []windowsUnsafeLookupCase{
		{name: "cwd", cwd: remote, path: `C:\bin`, request: LookupRequest{Command: toolDocker}},
		{name: "path", cwd: localCWD, path: remote, request: LookupRequest{Command: toolDocker}},
		{name: lookupTestReviewedLocation, cwd: safeCWD, request: LookupRequest{
			Command: toolDocker, ReviewedLocations: []string{remote + `\docker.exe`},
		}},
		{name: "explicit", cwd: safeCWD, request: LookupRequest{
			Command: remote + `\docker.exe`, Explicit: true,
		}},
		{name: "relative", cwd: remote, request: LookupRequest{
			Command: `bin\docker.exe`, Explicit: true,
		}},
		{name: "device", cwd: safeCWD, request: LookupRequest{
			Command: `\\.\pipe\docker.exe`, Explicit: true,
		}},
	}
}

func TestWindowsLookupUnsafeRequestLocations(t *testing.T) {
	for _, scenario := range unsafeWindowsLookupCases(t.TempDir()) {
		t.Run(scenario.name, func(t *testing.T) {
			environment, err := NewLookupEnvironment(map[string]string{
				lookupWindowsTestPATH: scenario.path,
			}, scenario.cwd)
			require.NoError(t, err)
			metadata := &lookupMetadataSpy{t: t, forbidCalls: true}
			environment.inspect = func(ctx context.Context, path string) (lookupFile, error) {
				if scenario.name == "path" && path == scenario.cwd {
					return inspectLookupFile(ctx, path)
				}
				return inspectWindowsLookup(ctx, path, metadata)
			}
			result, err := environment.LookupExecutable(context.Background(), scenario.request)
			require.NoError(t, err)
			require.Equal(t, LookupInspectionDeferred, result.ReasonCode)
			require.NotEmpty(t, result.Candidates)
			for _, candidate := range result.Candidates {
				require.Equal(t, FileDeferred, candidate.State)
				require.Equal(t, LookupInspectionDeferred, candidate.ReasonCode)
				require.Nil(t, candidate.Identity)
				require.Empty(t, candidate.ResolvedPath)
			}
			require.Empty(t, metadata.calls)
		})
	}
}

func TestWindowsLookupNativeLocalIdentityAndReparseDeferral(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "docker.exe")
	require.NoError(t, os.WriteFile(path, []byte("fixture"), 0o600))
	environment, err := NewLookupEnvironment(nil, root)
	require.NoError(t, err)
	request := LookupRequest{Command: path, Explicit: true}
	first, err := environment.LookupExecutable(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, first.Candidates, 1)
	require.Equal(t, FileExecutable, first.Candidates[0].State)
	second, err := environment.LookupExecutable(context.Background(), request)
	require.NoError(t, err)
	require.True(t, first.Candidates[0].Identity.SameFile(second.Candidates[0].Identity))

	link := filepath.Join(root, "local-link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("native symlink creation unavailable; local identity passed: %v", err)
	}
	result, err := environment.LookupExecutable(context.Background(), LookupRequest{
		Command: filepath.Join(link, "docker.exe"), Explicit: true,
	})
	require.NoError(t, err)
	require.Equal(t, LookupInspectionDeferred, result.ReasonCode)
	require.Len(t, result.Candidates, 1)
	require.Equal(t, FileDeferred, result.Candidates[0].State)
	require.True(t, result.Candidates[0].IsSymlink)
	require.Nil(t, result.Candidates[0].Identity)
	require.Empty(t, result.Candidates[0].ResolvedPath)
}

func TestWindowsLookupNativePATHRejectsCWDCaseAlias(t *testing.T) {
	root := t.TempDir()
	fallback := filepath.Join(root, "bin")
	require.NoError(t, os.Mkdir(fallback, 0o700))
	for _, directory := range []string{root, fallback} {
		require.NoError(
			t,
			os.WriteFile(filepath.Join(directory, "docker.exe"), []byte("fixture"), 0o600),
		)
	}
	environment, err := NewLookupEnvironment(map[string]string{
		lookupWindowsTestPATH: strings.ToUpper(root) + ";" + fallback, "PATHEXT": ".EXE",
	}, root)
	require.NoError(t, err)
	result, err := environment.LookupExecutable(
		context.Background(),
		LookupRequest{Command: toolDocker, Explicit: true},
	)
	require.NoError(t, err)
	require.Len(t, result.Candidates, 1)
	require.Equal(t, filepath.Join(fallback, "docker.EXE"), result.Candidates[0].LexicalPath)
	require.Equal(t, FileExecutable, result.Candidates[0].State)
}
