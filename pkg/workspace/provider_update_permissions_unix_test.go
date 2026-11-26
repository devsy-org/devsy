//go:build !windows

package workspace

import (
	"archive/tar"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProviderUpdateReadOnlyDirectorySeed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires POSIX permission enforcement for an ordinary user")
	}
	for _, failure := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "installed-only success", true: "download failure preserves live modes"}[failure],
			func(t *testing.T) {
				f := newPreservationFixture(t)
				content := []byte("installed-only executable")
				f.seedBinary(updateRuntimeKey, content)
				ancillary, modes := seedReadOnlyAncillaryTree(t, f)
				binary := updateTestBinary("unavailable-original-source/runtime", content)
				if failure {
					server := ownedProviderUpdateServer(
						t,
						func(w http.ResponseWriter, r *http.Request) {
							http.Error(w, "owned missing replacement", http.StatusNotFound)
						},
					)
					binary = updateTestBinary(server.URL+"/runtime", []byte("new replacement"))
				}
				err := f.update(map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}})
				if failure {
					require.ErrorContains(
						t,
						err,
						"404",
						"seeding must succeed before the concrete download failure",
					)
					f.assertFailedPreservation()
				} else {
					require.NoError(t, err)
					// #nosec G304 -- Ancillary path belongs to the isolated archive fixture.
					actual, readErr := os.ReadFile(ancillary)
					require.NoError(t, readErr)
					assert.Equal(t, f.before[ancillary], actual)
					assertSelectedExecutable(
						t,
						f.binaryPath(updateRuntimeKey),
						content,
						binary.Checksum,
					)
				}
				for path, mode := range modes {
					info, statErr := os.Stat(path)
					require.NoError(t, statErr)
					assert.Equal(t, mode, info.Mode().Perm(), path)
				}
				assertNoUpdateStaging(t, f)
			},
		)
	}
}

func seedReadOnlyAncillaryTree(
	t *testing.T,
	f *preservationFixture,
) (string, map[string]os.FileMode) {
	t.Helper()
	ancillary := filepath.Join(f.binaries, "unrelated", "read-only", "data")
	f.put(ancillary, []byte("unchanged ancillary bytes"))
	modes := map[string]os.FileMode{
		filepath.Dir(ancillary):                0o550,
		filepath.Join(f.binaries, "unrelated"): 0o770,
	}
	t.Cleanup(func() { makeOwnedUpdateTreeRemovable(t, f.dir) })
	for path, mode := range modes {
		require.NoError(t, os.Chmod(path, mode))
	}
	return ancillary, modes
}

func makeOwnedUpdateTreeRemovable(t *testing.T, directory string) {
	t.Helper()
	root, err := os.OpenRoot(directory)
	if !assert.NoError(t, err) {
		return
	}
	defer func() { assert.NoError(t, root.Close()) }()
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			// #nosec G302 -- Owner rwx is required to remove directories in this test-owned tree.
			return root.Chmod(name, 0o700)
		}
		return nil
	})
	assert.NoError(t, err, "make only this test-owned tree removable")
}

func TestProviderUpdateReadOnlySelectedArchiveFamily(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires POSIX permission enforcement for an ordinary user")
	}
	for _, failure := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "fresh download", true: "failed download"}[failure],
			func(t *testing.T) {
				f := newPreservationFixture(t)
				f.seedBinary(updateRuntimeKey, []byte("old selected runtime"))
				f.seedBinary("UNRELATED", []byte("unrelated runtime"))
				oldMember := seedReadOnlySelectedUpdateFamily(t, f)
				unrelatedInfo, err := os.Stat(f.binaryPath("UNRELATED"))
				require.NoError(t, err)
				content := []byte("fresh readonly-family replacement")
				archive := singleExecutableUpdateArchive(t, content)
				server := ownedProviderUpdateServer(
					t,
					func(w http.ResponseWriter, _ *http.Request) {
						if failure {
							http.Error(w, "owned replacement missing", http.StatusNotFound)
							return
						}
						_, _ = w.Write(archive)
					},
				)
				binary := updateTestBinary(server.URL+"/readonly.tar", content)
				binary.ArchivePath = updateRuntimeName
				err = f.update(map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}})
				if failure {
					require.ErrorContains(t, err, "404")
					f.assertFailedPreservation()
					memberInfo, statErr := os.Stat(oldMember)
					require.NoError(t, statErr)
					assert.Equal(t, os.FileMode(0o440), memberInfo.Mode().Perm())
					dirInfo, statErr := os.Stat(filepath.Dir(oldMember))
					require.NoError(t, statErr)
					assert.Equal(t, os.FileMode(0o550), dirInfo.Mode().Perm())
				} else {
					require.NoError(t, err)
					assertSelectedExecutable(
						t,
						f.binaryPath(updateRuntimeKey),
						content,
						binary.Checksum,
					)
					require.NoDirExists(t, filepath.Dir(oldMember))
				}
				actual, readErr := os.ReadFile(f.binaryPath("UNRELATED"))
				require.NoError(t, readErr)
				assert.Equal(t, f.before[f.binaryPath("UNRELATED")], actual)
				actualInfo, statErr := os.Stat(f.binaryPath("UNRELATED"))
				require.NoError(t, statErr)
				assert.Equal(t, unrelatedInfo.Mode().Perm(), actualInfo.Mode().Perm())
				assertNoUpdateStaging(t, f)
			},
		)
	}
}

func seedReadOnlySelectedUpdateFamily(t *testing.T, f *preservationFixture) string {
	t.Helper()
	oldMember := filepath.Join(f.binaries, updateRuntimeName, "old-readonly", "member")
	f.put(oldMember, []byte("obsolete selected-family sidecar"))
	t.Cleanup(func() { makeOwnedUpdateTreeRemovable(t, f.dir) })
	// #nosec G302 -- Owned fixture must exercise removal of a read-only selected family.
	require.NoError(t, os.Chmod(oldMember, 0o440))
	// #nosec G302 -- Owned fixture preserves readable, non-writable directory permissions.
	require.NoError(t, os.Chmod(filepath.Dir(oldMember), 0o550))
	return oldMember
}

func TestProviderUpdateReadOnlyNestedSelectedExecutable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires POSIX permission enforcement for an ordinary user")
	}
	f := newPreservationFixture(t)
	f.seedBinary("UNRELATED", []byte(updateUnrelatedContent))
	selected := "readonly/runtime"
	oldPath := filepath.Join(f.binaries, updateRuntimeName, filepath.FromSlash(selected))
	f.put(oldPath, []byte("old selected executable inside readonly directory"))
	t.Cleanup(func() { makeOwnedUpdateTreeRemovable(t, f.dir) })
	// #nosec G302 -- Owned executable deliberately has no owner-write permission.
	require.NoError(t, os.Chmod(oldPath, 0o550))
	// #nosec G302 -- Owned fixture directory must remain searchable but non-writable.
	require.NoError(t, os.Chmod(filepath.Dir(oldPath), 0o550))
	content := []byte("new selected executable after safe archive family reset")
	archive := compatibilityTar(t, false, []*tar.Header{
		{Name: "readonly", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: selected, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(content))},
	}, map[string][]byte{selected: content})
	var requests atomic.Int64
	server := ownedProviderUpdateServer(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(archive)
	})
	binary := updateTestBinary(server.URL+"/readonly-selected.tar", content)
	binary.ArchivePath = selected
	require.NoError(t, f.update(map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}}))
	require.EqualValues(
		t,
		1,
		requests.Load(),
		"fresh archive must be fetched without a seeded cache",
	)
	assertSelectedExecutable(t, oldPath, content, binary.Checksum)
	actual, err := os.ReadFile(f.binaryPath("UNRELATED"))
	require.NoError(t, err)
	assert.Equal(t, f.before[f.binaryPath("UNRELATED")], actual)
	f.assertAssociation()
	assertNoUpdateStaging(t, f)
}
