//go:build !windows

package workspace

import (
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

type readOnlyDirectUpdateCase struct {
	name          string
	missingLeaf   bool
	missingFamily bool
	failure       bool
	reuse         bool
}

func TestProviderUpdateDirectBinaryUnderReadOnlyParent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires POSIX permission enforcement for an ordinary user")
	}

	cases := []readOnlyDirectUpdateCase{
		{name: "replace existing leaf"},
		{name: "failed download restores modes", failure: true},
		{name: "matching checksum reuses installed leaf", reuse: true},
		{name: "create missing leaf", missingLeaf: true},
		{name: "create missing family", missingFamily: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runReadOnlyDirectUpdateCase(t, testCase)
		})
	}
}

func runReadOnlyDirectUpdateCase(t *testing.T, testCase readOnlyDirectUpdateCase) {
	t.Helper()
	f, selected, parent := seedReadOnlyDirectUpdateCase(t, testCase)
	old := []byte("old owner-writable selected executable")
	updated := []byte("new direct remote executable")
	if testCase.reuse {
		updated = old
	}
	var requests atomic.Int64
	server := ownedProviderUpdateServer(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		if testCase.failure {
			http.Error(w, "owned replacement unavailable", http.StatusNotFound)
			return
		}
		_, _ = w.Write(updated)
	})
	binary := updateTestBinary(server.URL+"/runtime", updated)
	err := f.update(map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}})
	if testCase.failure {
		assertFailedReadOnlyDirectUpdate(t, f, err, requests.Load())
	} else {
		require.NoError(t, err)
		wantRequests := int64(1)
		if testCase.reuse {
			wantRequests = 0
		}
		require.EqualValues(t, wantRequests, requests.Load())
		assertSelectedExecutable(t, selected, updated, binary.Checksum)
		f.assertAssociation()
	}
	if !testCase.missingFamily {
		info, statErr := os.Stat(parent)
		require.NoError(t, statErr)
		require.Equal(
			t,
			os.FileMode(0o550),
			info.Mode().Perm(),
			"staged parent mode must be restored before publication",
		)
	}
	assertNoUpdateStaging(t, f)
}

func seedReadOnlyDirectUpdateCase(
	t *testing.T,
	testCase readOnlyDirectUpdateCase,
) (*preservationFixture, string, string) {
	t.Helper()
	f := newPreservationFixture(t)
	f.seedBinary(updateRuntimeKey, []byte("old owner-writable selected executable"))
	selected := f.binaryPath(updateRuntimeKey)
	parent := filepath.Dir(selected)
	t.Cleanup(func() { makeOwnedUpdateTreeRemovable(t, f.dir) })
	if testCase.missingFamily {
		require.NoError(t, os.RemoveAll(parent))
	} else {
		if testCase.missingLeaf {
			require.NoError(t, os.Remove(selected))
		} else {
			// #nosec G302 -- The fixture reproduces a writable file below a read-only parent.
			require.NoError(t, os.Chmod(selected, 0o700))
		}
		// #nosec G302 -- The fixture reproduces ordinary staged-copy permission preservation.
		require.NoError(t, os.Chmod(parent, 0o550))
	}
	return f, selected, parent
}

func assertFailedReadOnlyDirectUpdate(
	t *testing.T,
	f *preservationFixture,
	err error,
	requests int64,
) {
	t.Helper()
	require.ErrorContains(t, err, "404")
	require.EqualValues(t, 1, requests)
	f.assertFailedPreservation()
}

func TestDownloadUpdateBinaryUnderReadOnlyStagingRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires POSIX permission enforcement for an ordinary user")
	}

	for _, missingFamily := range []bool{false, true} {
		name := "existing read-only family"
		if missingFamily {
			name = "missing family"
		}
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "binaries")
			require.NoError(t, os.Mkdir(root, 0o700))
			selected := filepath.Join(root, "runtime", updateRuntimeName)
			if !missingFamily {
				require.NoError(t, os.Mkdir(filepath.Dir(selected), 0o700))
				// #nosec G306 -- Test input is intentionally owner-writable under a read-only directory.
				require.NoError(t, os.WriteFile(selected, []byte("old staged bytes"), 0o700))
				// #nosec G302 -- Exercise permission preservation for an existing nested staged directory.
				require.NoError(t, os.Chmod(filepath.Dir(selected), 0o550))
			}
			unrelated := filepath.Join(root, "unrelated", "kept")
			require.NoError(t, os.MkdirAll(filepath.Dir(unrelated), 0o700))
			require.NoError(t, os.WriteFile(unrelated, []byte("unrelated staged data"), 0o600))
			t.Cleanup(func() { makeOwnedUpdateTreeRemovable(t, root) })
			// #nosec G302 -- The staged root is deliberately read-only for the direct downloader.
			require.NoError(t, os.Chmod(root, 0o550))

			updated := []byte("replacement staged binary")
			server := ownedProviderUpdateServer(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(updated)
			})
			binary := updateTestBinary(server.URL+"/runtime", updated)
			paths, err := provider.DownloadUpdateBinaries(
				t.Context(),
				map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}},
				root,
			)
			require.NoError(t, err)
			require.Equal(t, selected, paths[updateRuntimeKey])
			assertSelectedExecutable(t, selected, updated, binary.Checksum)
			actual, err := os.ReadFile(unrelated) // #nosec G304 -- Fixture-owned stage file.
			require.NoError(t, err)
			require.Equal(t, []byte("unrelated staged data"), actual)
			info, err := os.Stat(root)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o550), info.Mode().Perm())
			if !missingFamily {
				info, err = os.Stat(filepath.Dir(selected))
				require.NoError(t, err)
				require.Equal(t, os.FileMode(0o550), info.Mode().Perm())
			}
		})
	}
}
