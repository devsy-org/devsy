package workspace

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// compatibilityArchiveServer serves bytes from an owned httptest server. The
// common fixture cleanup closes idle connections and joins the server.
func compatibilityArchiveServer(t *testing.T, archive []byte) *httptest.Server {
	t.Helper()
	return ownedProviderUpdateServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
}

func compatibilityTar(
	t *testing.T,
	gzipArchive bool,
	entries []*tar.Header,
	bodies map[string][]byte,
) []byte {
	t.Helper()
	var out bytes.Buffer
	var writer io.Writer = &out
	var gz *gzip.Writer
	if gzipArchive {
		gz = gzip.NewWriter(&out)
		writer = gz
	}
	tarWriter := tar.NewWriter(writer)
	for _, header := range entries {
		require.NoError(t, tarWriter.WriteHeader(header))
		if body := bodies[header.Name]; len(body) != 0 {
			_, err := tarWriter.Write(body)
			require.NoError(t, err)
		}
	}
	require.NoError(t, tarWriter.Close())
	if gz != nil {
		require.NoError(t, gz.Close())
	}
	return out.Bytes()
}

func singleExecutableUpdateArchive(t *testing.T, selected string, content []byte) []byte {
	t.Helper()
	return compatibilityTar(t, false, []*tar.Header{
		{Name: selected, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(content))},
	}, map[string][]byte{selected: content})
}

func compatibilityZip(t *testing.T, entries []struct {
	name string
	mode os.FileMode
	body []byte
},
) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, item := range entries {
		header := &zip.FileHeader{Name: item.name}
		header.SetMode(item.mode)
		member, err := writer.CreateHeader(header)
		require.NoError(t, err)
		_, err = member.Write(item.body)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return out.Bytes()
}

func assertSelectedExecutable(t *testing.T, path string, content []byte, checksum string) {
	t.Helper()
	actual, err := os.ReadFile(
		path,
	) // #nosec G304 -- Selected executable belongs to the isolated update fixture.
	require.NoError(t, err)
	assert.Equal(t, content, actual)
	info, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS != updateWindowsOS {
		assert.NotZero(
			t,
			info.Mode().Perm()&0o111,
			"selected executable should retain executable permission",
		)
	}
	assert.Equal(
		t,
		checksum,
		updateTestDigest(actual),
		"selected archive member should match its declared checksum",
	)
}

func TestProviderUpdateTarAndTgzCompatibility(t *testing.T) {
	for _, gzipArchive := range []bool{false, true} {
		t.Run(map[bool]string{false: "tar", true: "tgz"}[gzipArchive], func(t *testing.T) {
			f := newPreservationFixture(t)
			f.seedBinary("UNRELATED", []byte("keep unrelated installed binary"))
			runtimeBytes := []byte("ordinary archive runtime bytes")
			sidecarBytes := []byte("ordinary archive sibling sidecar")
			archive := compatibilityTar(t, gzipArchive, []*tar.Header{
				{
					Name:     updateRuntimeName,
					Typeflag: tar.TypeReg,
					Mode:     0o755,
					Size:     int64(len(runtimeBytes)),
				},
				{
					Name:     updateSidecarPath,
					Typeflag: tar.TypeReg,
					Mode:     0o750,
					Size:     int64(len(sidecarBytes)),
				},
			}, map[string][]byte{updateRuntimeName: runtimeBytes, updateSidecarPath: sidecarBytes})
			extension := ".tar"
			if gzipArchive {
				extension = ".tgz"
			}
			server := compatibilityArchiveServer(t, archive)
			binary := updateTestBinary(server.URL+"/provider"+extension, runtimeBytes)
			binary.ArchivePath = updateRuntimeName
			require.NoError(
				t,
				f.update(map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}}),
			)
			assertSelectedExecutable(
				t,
				f.binaryPath(updateRuntimeKey),
				runtimeBytes,
				binary.Checksum,
			)
			sidecar := filepath.Join(f.binaries, updateRuntimeName, "lib", "sidecar")
			actual, err := os.ReadFile(
				sidecar,
			) // #nosec G304 -- Sidecar belongs to the isolated update fixture.
			require.NoError(t, err)
			assert.Equal(t, sidecarBytes, actual)
			sidecarInfo, err := os.Stat(sidecar)
			require.NoError(t, err)
			if runtime.GOOS != updateWindowsOS {
				assert.Equal(
					t,
					os.FileMode(0o750),
					sidecarInfo.Mode().Perm(),
					"ancillary executable mode must survive",
				)
			}
			unrelated, err := os.ReadFile(f.binaryPath("UNRELATED"))
			require.NoError(t, err)
			assert.Equal(t, []byte("keep unrelated installed binary"), unrelated)
		})
	}
}

func TestProviderUpdateZipCompatibility(t *testing.T) {
	for _, includeLink := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "plain archive", true: "contained file symlink"}[includeLink],
			func(t *testing.T) {
				if includeLink {
					requireProviderUpdateSymlinks(t)
				}
				assertProviderUpdateZipCompatibility(t, includeLink)
			},
		)
	}
}

func assertProviderUpdateZipCompatibility(t *testing.T, includeLink bool) {
	t.Helper()
	f := newPreservationFixture(t)
	runtimeBytes := []byte("ordinary zip runtime bytes")
	sidecarBytes := []byte("ordinary zip ancillary sibling")
	entries := []struct {
		name string
		mode os.FileMode
		body []byte
	}{
		{name: updateRuntimeName, mode: os.ModePerm | 0o755, body: runtimeBytes},
		{name: updateSidecarPath, mode: os.ModePerm | 0o644, body: sidecarBytes},
	}
	if includeLink {
		entries = append(entries, struct {
			name string
			mode os.FileMode
			body []byte
		}{
			name: "runtime-link", mode: os.ModeSymlink | 0o777, body: []byte(updateRuntimeName),
		})
	}
	archive := compatibilityZip(t, entries)
	server := compatibilityArchiveServer(t, archive)
	binary := updateTestBinary(server.URL+"/provider.zip", runtimeBytes)
	binary.ArchivePath = updateRuntimeName
	require.NoError(t, f.update(map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}}))
	assertSelectedExecutable(t, f.binaryPath(updateRuntimeKey), runtimeBytes, binary.Checksum)
	if includeLink {
		link := filepath.Join(f.binaries, updateRuntimeName, "runtime-link")
		target, err := os.Readlink(link)
		require.NoError(t, err)
		assert.Equal(t, updateRuntimeName, target)
		linked, err := os.ReadFile(
			link,
		) // #nosec G304 -- Contained link belongs to the isolated update fixture.
		require.NoError(t, err)
		assert.Equal(t, runtimeBytes, linked)
	}
	sidecar, err := os.ReadFile(filepath.Join(f.binaries, updateRuntimeName, "lib", "sidecar"))
	require.NoError(t, err)
	assert.Equal(t, sidecarBytes, sidecar)
}

func TestProviderUpdateTarContainedDirectoryLinkThenRegularMember(t *testing.T) {
	requireProviderUpdateSymlinks(t)
	f := newPreservationFixture(t)
	runtimeBytes := []byte("runtime beneath contained directory link")
	sidecarBytes := []byte("sibling reached through contained directory link")
	archive := compatibilityTar(t, false, []*tar.Header{
		{Name: "releases", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "releases/v1", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: updateCurrentLink, Typeflag: tar.TypeSymlink, Linkname: "releases/v1", Mode: 0o777},
		{
			Name:     "current/runtime",
			Typeflag: tar.TypeReg,
			Mode:     0o755,
			Size:     int64(len(runtimeBytes)),
		},
		{
			Name:     "current/lib/sidecar",
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len(sidecarBytes)),
		},
	}, map[string][]byte{"current/runtime": runtimeBytes, "current/lib/sidecar": sidecarBytes})
	server := compatibilityArchiveServer(t, archive)
	binary := updateTestBinary(server.URL+"/provider.tar", runtimeBytes)
	binary.ArchivePath = filepath.Join(updateCurrentLink, updateRuntimeName)
	require.NoError(t, f.update(map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}}))
	assertSelectedExecutable(
		t,
		filepath.Join(f.binaries, updateRuntimeName, updateCurrentLink, updateRuntimeName),
		runtimeBytes,
		binary.Checksum,
	)
	link := filepath.Join(f.binaries, updateRuntimeName, updateCurrentLink)
	target, err := os.Readlink(link)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("releases", "v1"), filepath.FromSlash(target))
	actual, err := os.ReadFile(
		filepath.Join(f.binaries, updateRuntimeName, "releases", "v1", "lib", "sidecar"),
	)
	require.NoError(t, err)
	assert.Equal(t, sidecarBytes, actual)
}

func TestProviderUpdateTarHardlinkCompatibility(t *testing.T) {
	f := newPreservationFixture(t)
	runtimeBytes := []byte("hard-linked executable bytes")
	archive := compatibilityTar(t, true, []*tar.Header{
		{
			Name:     updateRuntimeName,
			Typeflag: tar.TypeReg,
			Linkname: "",
			Mode:     0o755,
			Size:     int64(len(runtimeBytes)),
		},
		{Name: "runtime-copy", Typeflag: tar.TypeLink, Linkname: updateRuntimeName, Mode: 0o755},
	}, map[string][]byte{updateRuntimeName: runtimeBytes})
	server := compatibilityArchiveServer(t, archive)
	binary := updateTestBinary(server.URL+"/provider.tgz", runtimeBytes)
	binary.ArchivePath = updateRuntimeName
	require.NoError(t, f.update(map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}}))
	assertSelectedExecutable(t, f.binaryPath(updateRuntimeKey), runtimeBytes, binary.Checksum)
	copyPath := filepath.Join(f.binaries, updateRuntimeName, "runtime-copy")
	// #nosec G304 -- Hardlink bytes are read only from the isolated archive fixture.
	copyBytes, err := os.ReadFile(copyPath)
	require.NoError(t, err)
	assert.Equal(t, runtimeBytes, copyBytes)
	selectedInfo, err := os.Stat(f.binaryPath(updateRuntimeKey))
	require.NoError(t, err)
	copyInfo, err := os.Stat(copyPath)
	require.NoError(t, err)
	assert.True(
		t,
		os.SameFile(selectedInfo, copyInfo),
		"tar hardlink should remain a hardlink after update preparation",
	)
}

func TestProviderUpdateContainedSeedLinkInstalledOnlyReuse(t *testing.T) {
	requireProviderUpdateSymlinks(t)
	f := newPreservationFixture(t)
	content := []byte("installed executable reachable through a contained seed link")
	versionDir := filepath.Join(f.binaries, updateRuntimeName, "releases", "v1")
	// #nosec G301 -- The owned fixture reproduces a searchable installed binary directory.
	require.NoError(t, os.MkdirAll(versionDir, 0o755))
	versionBinary := filepath.Join(versionDir, updateRuntimeName)
	// #nosec G306 -- This test checks reuse of an installed executable with its executable mode.
	require.NoError(t, os.WriteFile(versionBinary, content, 0o755))
	link := filepath.Join(f.binaries, updateRuntimeName, updateCurrentLink)
	require.NoError(t, os.Symlink(filepath.Join("releases", "v1"), link))
	binaryPath := filepath.Join(updateCurrentLink, updateRuntimeName)
	binary := updateTestBinary("https://offline.invalid/provider.tgz", content)
	binary.ArchivePath = binaryPath
	require.NoError(t, f.update(map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}}))
	assertSelectedExecutable(
		t,
		filepath.Join(f.binaries, updateRuntimeName, binaryPath),
		content,
		binary.Checksum,
	)
	target, err := os.Readlink(link)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("releases", "v1"), filepath.FromSlash(target))
	// #nosec G304 -- Installed executable belongs to the isolated update fixture.
	actual, err := os.ReadFile(versionBinary)
	require.NoError(t, err)
	assert.Equal(t, content, actual)
}

func TestProviderUpdateSelectedArchiveChecksumMismatchDoesNotPublish(t *testing.T) {
	f := newPreservationFixture(t)
	f.seedBinary(updateRuntimeKey, []byte("previous selected executable"))
	selected := []byte("new archive selected executable")
	archive := compatibilityTar(t, true, []*tar.Header{
		{Name: updateRuntimeName, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(selected))},
		{Name: updateSidecarPath, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(selected))},
	}, map[string][]byte{updateRuntimeName: selected, updateSidecarPath: selected})
	server := compatibilityArchiveServer(t, archive)
	binary := updateTestBinary(server.URL+"/provider.tgz", []byte("wrong selected checksum bytes"))
	binary.ArchivePath = updateRuntimeName
	err := f.update(map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}})
	require.ErrorContains(t, err, "checksum")
	f.assertFailedPreservation()
}

func preparedUpdateParams(
	t *testing.T,
	f *preservationFixture,
	binaries map[string][]*provider.ProviderBinary,
) (ProviderParams, *provider.ProviderConfig) {
	t.Helper()
	replacement := &provider.ProviderConfig{
		Name:    f.name,
		Version: updateReplacementVersion,
		Exec:    provider.ProviderCommands{Command: []string{updateNoopCommand}},
		Options: map[string]*types.Option{
			updateKeptOption: {},
		},
		Binaries: binaries,
		Source:   provider.ProviderSource{Raw: "candidate-new"},
	}
	raw, err := json.Marshal(replacement)
	require.NoError(t, err)
	return ProviderParams{
		DevsyConfig:  f.config,
		ProviderName: f.name,
		Raw:          raw,
		Source:       &replacement.Source,
	}, replacement
}

func assertNoUpdateStaging(t *testing.T, f *preservationFixture) {
	t.Helper()
	entries, err := os.ReadDir(f.dir)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.False(t, strings.HasPrefix(entry.Name(), ".update-staging-"), entry.Name())
	}
}

func TestProviderUpdateChecksumFailure(t *testing.T) {
	f := newPreservationFixture(t)
	f.seedBinary(updateRuntimeKey, []byte("old"))
	server := ownedProviderUpdateServer(
		t,
		func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("bad checksum")) },
	)
	err := f.update(
		map[string][]*provider.ProviderBinary{
			updateRuntimeKey: {updateTestBinary(server.URL+"/runtime", []byte("expected"))},
		},
	)
	require.Error(t, err)
	f.assertFailedPreservation()
	assertNoUpdateStaging(t, f)
}

func TestProviderUpdateCancellation(t *testing.T) {
	f := newPreservationFixture(t)
	f.seedBinary(updateRuntimeKey, []byte("old"))
	entered, handlerDone := make(chan struct{}), make(chan struct{})
	server := ownedProviderUpdateServer(t, func(_ http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		close(entered)
		<-r.Context().Done()
	})
	params, _ := preparedUpdateParams(
		t,
		f,
		map[string][]*provider.ProviderBinary{
			updateRuntimeKey: {updateTestBinary(server.URL+"/runtime", []byte("new"))},
		},
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		_, err := updateProvider(ctx, params)
		done <- err
	}()
	cleanupProviderUpdateCaller(t, cancel, joined)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("update did not join")
	}
	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not join")
	}
	f.assertFailedPreservation()
	assertNoUpdateStaging(t, f)
}

func cleanupProviderUpdateCaller(t *testing.T, cancel context.CancelFunc, joined <-chan struct{}) {
	t.Helper()
	t.Cleanup(func() {
		cancel()
		select {
		case <-joined:
		case <-time.After(3 * time.Second):
			t.Error("update caller did not join during cleanup")
		}
	})
}

func TestProviderUpdateRejectsExistingSymlink(t *testing.T) {
	requireProviderUpdateSymlinks(t)
	f := newPreservationFixture(t)
	f.seedBinary(updateRuntimeKey, []byte("old"))
	external := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(external, []byte("untouched"), 0o600))
	require.NoError(t, os.Symlink(external, filepath.Join(f.binaries, "outside-link")))
	err := f.update(
		map[string][]*provider.ProviderBinary{
			updateRuntimeKey: {updateTestBinary("absent-old-source", []byte("old"))},
		},
	)
	require.ErrorContains(t, err, "unsafe provider update symlink")
	// #nosec G304 -- Outside sentinel is a separate test-owned temporary file.
	data, err := os.ReadFile(external)
	require.NoError(t, err)
	assert.Equal(t, "untouched", string(data))
	f.assertFailedPreservation()
	assertNoUpdateStaging(t, f)
}

func TestProviderUpdateValidAbsoluteAndNoBinary(t *testing.T) {
	for _, absolute := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "no binaries", true: "absolute reference"}[absolute],
			func(t *testing.T) {
				f := newPreservationFixture(t)
				f.seedBinary("OLD", []byte("preserved unused bytes"))
				binaries := map[string][]*provider.ProviderBinary{}
				if absolute {
					path := filepath.Join(t.TempDir(), "external")
					f.put(path, []byte("external"))
					binaries[updateRuntimeKey] = []*provider.ProviderBinary{
						updateTestBinary(path, []byte("external")),
					}
				}
				require.NoError(t, f.update(binaries))
				for path, before := range f.before {
					if filepath.Base(path) == provider.ProviderConfigFile {
						continue
					}
					// #nosec G304 -- Snapshot path comes only from the test-owned fixture.
					after, err := os.ReadFile(path)
					require.NoError(t, err)
					assert.Equal(t, before, after)
				}
				state := f.assertAssociation()
				assert.False(t, state.Initialized)
				assert.Equal(
					t,
					map[string]config.OptionValue{updateKeptOption: f.options[updateKeptOption]},
					state.Options,
				)
				assertNoUpdateStaging(t, f)
			},
		)
	}
}

func TestProviderUpdateArchiveSuccessKeepsSiblings(t *testing.T) {
	f := newPreservationFixture(t)
	f.seedBinary(updateRuntimeKey, []byte("old"))
	f.seedBinary("UNRELATED", []byte(updateUnrelatedContent))
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gz)
	for name, data := range map[string]string{
		updateRuntimeName: updateExecutableContent,
		updateSidecarPath: "required sidecar",
	} {
		require.NoError(
			t,
			tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}),
		)
		_, err := tarWriter.Write([]byte(data))
		require.NoError(t, err)
	}
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gz.Close())
	server := ownedProviderUpdateServer(
		t,
		func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive.Bytes()) },
	)
	binary := updateTestBinary(server.URL+"/runtime.tar.gz", []byte(updateExecutableContent))
	binary.ArchivePath = updateRuntimeName
	require.NoError(t, f.update(map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}}))
	for path, want := range map[string]string{
		f.binaryPath(updateRuntimeKey):                                 updateExecutableContent,
		filepath.Join(f.binaries, updateRuntimeName, "lib", "sidecar"): "required sidecar",
		f.binaryPath("UNRELATED"):                                      updateUnrelatedContent,
		filepath.Join(f.dir, "daemon", "owned-state"):                  "untouched daemon state",
	} {
		// #nosec G304 -- Snapshot path comes only from the test-owned fixture.
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, want, string(got))
	}
	assertNoUpdateStaging(t, f)
}

func TestProviderUpdateSynchronousCommitRestoration(t *testing.T) {
	for _, phase := range []string{
		updateBackupPhase, updatePromotionPhase, "manifest after save", "config before save", updateRestoreFailure,
	} {
		t.Run(phase, func(t *testing.T) {
			f := newPreservationFixture(t)
			f.seedBinary(updateRuntimeKey, []byte("old"))
			params, replacement := preparedUpdateParams(
				t,
				f,
				map[string][]*provider.ProviderBinary{
					updateRuntimeKey: {
						updateTestBinary("unavailable-original-source/runtime", []byte("old")),
					},
				},
			)
			prepared, err := prepareProviderReplacement(context.Background(), params, replacement)
			require.NoError(t, err)
			defer func() { require.NoError(t, os.RemoveAll(prepared.stage)) }()
			sentinel := errors.New("owned injected " + phase)
			ops := updateCommitFailureOps(phase, sentinel)
			err = prepared.commit(params, replacement, ops)
			require.ErrorIs(t, err, sentinel)
			if phase == updateRestoreFailure {
				require.True(t, prepared.retain)
				backup, readErr := os.ReadFile(filepath.Join(prepared.stage, "old-provider.json"))
				require.NoError(t, readErr)
				assert.Equal(t, f.before[prepared.manifest], backup)
				require.ErrorContains(t, err, "owned restore failure")
			} else {
				require.False(t, prepared.retain)
				f.assertFailedPreservation()
			}
		})
	}
}

func updateCommitFailureOps(phase string, sentinel error) providerUpdateCommitOps {
	ops := defaultProviderUpdateCommitOps()
	switch phase {
	case updateBackupPhase, updatePromotionPhase:
		failAt := map[string]int{updateBackupPhase: 1, updatePromotionPhase: 2}[phase]
		originalRename := ops.rename
		calls := 0
		ops.rename = func(from, to string) error {
			calls++
			if calls == failAt {
				return sentinel
			}
			return originalRename(from, to)
		}
	case "manifest after save":
		ops.saveManifest = func(context string, p *provider.ProviderConfig) error {
			if err := provider.SaveProviderConfig(context, p); err != nil {
				return err
			}
			return sentinel
		}
	case "config before save", updateRestoreFailure:
		ops.updateConfig = func(string, string, func(*config.Config) error) error { return sentinel }
	}
	if phase == updateRestoreFailure {
		ops.restoreManifest = func(string, []byte, fs.FileMode) error { return errors.New("owned restore failure") }
	}
	return ops
}

func TestProviderUpdateCachedReplacement(t *testing.T) {
	f := newPreservationFixture(t)
	f.seedBinary(updateRuntimeKey, []byte("old"))
	content := []byte("new cached replacement " + f.dir)
	server := ownedProviderUpdateServer(
		t,
		func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(content) },
	)
	binaries := map[string][]*provider.ProviderBinary{
		updateRuntimeKey: {updateTestBinary(server.URL+"/runtime", content)},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := provider.DownloadBinaries(ctx, binaries, t.TempDir())
	require.NoError(t, err)
	server.Close()
	require.NoError(t, f.update(binaries))
	actual, err := os.ReadFile(f.binaryPath(updateRuntimeKey))
	require.NoError(t, err)
	assert.Equal(t, content, actual)
	assertNoUpdateStaging(t, f)
}

func TestProviderUpdateRejectsEscapingBinaryTarget(t *testing.T) {
	f := newPreservationFixture(t)
	f.seedBinary(updateRuntimeKey, []byte("old"))
	declaration := updateTestBinary("https://owned.invalid/runtime", []byte("new"))
	declaration.Name = "../../../../daemon/owned-state"
	err := f.update(map[string][]*provider.ProviderBinary{updateRuntimeKey: {declaration}})
	require.ErrorContains(t, err, "unsafe provider update binary target")
	f.assertFailedPreservation()
	assertNoUpdateStaging(t, f)
}

func TestProviderUpdateCachedArchiveReplacement(t *testing.T) {
	f := newPreservationFixture(t)
	f.seedBinary(updateRuntimeKey, []byte("old selected executable"))
	sidecar := filepath.Join(f.binaries, updateRuntimeName, "lib", "seeded-sidecar")
	f.put(sidecar, []byte("existing sidecar retained by legacy executable cache"))
	content := []byte("cached archive replacement " + f.dir)
	archive := compatibilityTar(t, true, []*tar.Header{
		{Name: updateRuntimeName, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(content))},
	}, map[string][]byte{updateRuntimeName: content})
	server := compatibilityArchiveServer(t, archive)
	binary := updateTestBinary(server.URL+"/provider.tgz", content)
	binary.ArchivePath = updateRuntimeName
	binaries := map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := provider.DownloadBinaries(ctx, binaries, t.TempDir())
	require.NoError(t, err)
	server.Close()
	require.NoError(
		t,
		f.update(binaries),
		"closed archive server requires existing executable cache reuse",
	)
	assertSelectedExecutable(t, f.binaryPath(updateRuntimeKey), content, binary.Checksum)
	actual, err := os.ReadFile(
		sidecar,
	) // #nosec G304 -- Sidecar belongs to the isolated update fixture.
	require.NoError(t, err)
	assert.Equal(t, f.before[sidecar], actual)
	assertNoUpdateStaging(t, f)
}

func TestProviderUpdateReadOnlyDirectorySeed(t *testing.T) {
	if runtime.GOOS == updateWindowsOS || os.Geteuid() == 0 {
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

func TestProviderUpdateContainedSeedDirectoryLinkChain(t *testing.T) {
	requireProviderUpdateSymlinks(t)
	f := newPreservationFixture(t)
	content := []byte("installed executable reached through a directory link chain")
	base := filepath.Join(f.binaries, updateRuntimeName)
	f.put(filepath.Join(base, "releases", "v1", updateRuntimeName), content)
	// Create source links in dependency order; staging must not depend on map iteration order.
	targets := map[string]string{
		updateCurrentLink: filepath.Join("releases", "v1"),
		updateMiddleLink:  updateCurrentLink,
		updateEntryLink:   updateMiddleLink,
	}
	for _, name := range []string{updateCurrentLink, updateMiddleLink, updateEntryLink} {
		require.NoError(t, os.Symlink(targets[name], filepath.Join(base, name)))
	}
	binary := updateTestBinary("https://offline.invalid/linked-provider.tgz", content)
	binary.ArchivePath = filepath.Join(updateEntryLink, updateRuntimeName)
	require.NoError(t, f.update(map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}}))
	for name, want := range targets {
		link := filepath.Join(base, name)
		target, err := os.Readlink(link)
		require.NoError(t, err)
		assert.Equal(t, want, target)
		info, err := os.Stat(link)
		require.NoError(t, err)
		assert.True(t, info.IsDir(), "seed link %s must retain directory traversal", name)
		assertSelectedExecutable(
			t,
			filepath.Join(link, updateRuntimeName),
			content,
			binary.Checksum,
		)
	}
	assertNoUpdateStaging(t, f)
}

func TestProviderUpdateFailedBinaryRestorationRetainsBackup(t *testing.T) {
	f := newPreservationFixture(t)
	content := []byte("old executable retained for recovery")
	f.seedBinary(updateRuntimeKey, content)
	binaries := map[string][]*provider.ProviderBinary{
		updateRuntimeKey: {updateTestBinary("unavailable-original-source/runtime", content)},
	}
	params, replacement := preparedUpdateParams(t, f, binaries)
	prepared, err := prepareProviderReplacement(context.Background(), params, replacement)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(prepared.stage)) })
	commitErr := errors.New("owned config commit failure")
	restoreErr := errors.New("owned binary backup restoration failure")
	ops := defaultProviderUpdateCommitOps()
	ops.updateConfig = func(string, string, func(*config.Config) error) error { return commitErr }
	rename := ops.rename
	backup := filepath.Join(prepared.stage, updateBackupPhase)
	ops.rename = func(from, to string) error {
		if from == backup {
			return restoreErr
		}
		return rename(from, to)
	}
	err = prepared.commit(params, replacement, ops)
	require.ErrorIs(t, err, commitErr)
	require.ErrorIs(t, err, restoreErr)
	require.True(t, prepared.retain, "failed restoration must retain recoverable artifacts")
	assertSelectedExecutable(
		t,
		filepath.Join(backup, updateRuntimeName, updateRuntimeName),
		content,
		updateTestDigest(content),
	)
	// #nosec G304 -- Manifest belongs to the isolated prepared-update fixture.
	manifest, err := os.ReadFile(prepared.manifest)
	require.NoError(t, err)
	assert.Equal(t, f.before[prepared.manifest], manifest, "manifest restoration still runs")
	state := f.assertAssociation()
	assert.True(t, state.Initialized)
	assert.True(t, state.InitAttempted)
	assert.Equal(t, f.options, state.Options)
	require.DirExists(t, filepath.Join(prepared.stage, "failed-new"))
	require.NoDirExists(
		t,
		prepared.live,
		"failed rename must not claim the old binaries were restored",
	)
}

func TestProviderUpdatePAXGlobalMetadata(t *testing.T) {
	f := newPreservationFixture(t)
	f.seedBinary(updateRuntimeKey, []byte("old PAX runtime"))
	content := []byte("new executable after global metadata")
	const metadata = "global-metadata"
	archive := compatibilityTar(t, false, []*tar.Header{
		{
			Name:       metadata,
			Typeflag:   tar.TypeXGlobalHeader,
			PAXRecords: map[string]string{"comment": "owned global metadata"},
		},
		{Name: updateRuntimeName, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(content))},
	}, map[string][]byte{updateRuntimeName: content})
	server := compatibilityArchiveServer(t, archive)
	binary := updateTestBinary(server.URL+"/global.tar", content)
	binary.ArchivePath = updateRuntimeName
	err := f.update(map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}})
	t.Logf("PAX global-header update result: %v", err)
	require.NoError(t, err)
	assertSelectedExecutable(t, f.binaryPath(updateRuntimeKey), content, binary.Checksum)
	require.NoFileExists(t, filepath.Join(f.binaries, updateRuntimeName, metadata))
	require.NoDirExists(t, filepath.Join(f.binaries, updateRuntimeName, metadata))
	assertNoUpdateStaging(t, f)
}

const updateArchiveLink = "runtime-link"

func linkedReplacementArchive(t *testing.T, linkType byte, content []byte, extra string) []byte {
	t.Helper()
	entries := []*tar.Header{
		{Name: updateRuntimeName, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(content))},
		{Name: updateArchiveLink, Typeflag: linkType, Linkname: updateRuntimeName, Mode: 0o777},
		{Name: updateSidecarPath, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))},
	}
	bodies := map[string][]byte{updateRuntimeName: content, updateSidecarPath: content}
	if extra != "" {
		entries = append(
			entries,
			&tar.Header{Name: extra, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))},
		)
		bodies[extra] = content
	}
	return compatibilityTar(t, false, entries, bodies)
}

func assertLinkedReplacement(t *testing.T, f *preservationFixture, linkType byte, content []byte) {
	t.Helper()
	selected := f.binaryPath(updateRuntimeKey)
	assertSelectedExecutable(t, selected, content, updateTestDigest(content))
	link := filepath.Join(f.binaries, updateRuntimeName, updateArchiveLink)
	// #nosec G304 -- Link is part of this test-owned provider archive.
	linked, err := os.ReadFile(link)
	require.NoError(t, err)
	assert.Equal(t, content, linked)
	if linkType == tar.TypeSymlink {
		target, err := os.Readlink(link)
		require.NoError(t, err)
		assert.Equal(t, updateRuntimeName, target)
	} else {
		selectedInfo, err := os.Stat(selected)
		require.NoError(t, err)
		linkInfo, err := os.Stat(link)
		require.NoError(t, err)
		assert.True(t, os.SameFile(selectedInfo, linkInfo))
	}
	sidecar, err := os.ReadFile(filepath.Join(f.binaries, updateRuntimeName, "lib", "sidecar"))
	require.NoError(t, err)
	assert.Equal(t, content, sidecar)
	for _, path := range []string{
		f.binaryPath("UNRELATED"),
		filepath.Join(f.dir, "owned-sentinel"),
		filepath.Join(f.dir, "daemon", "owned-state"),
	} {
		// #nosec G304 -- These paths were populated and snapshotted by this isolated fixture.
		actual, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, f.before[path], actual)
	}
	assert.False(t, f.assertAssociation().Initialized)
	assertNoUpdateStaging(t, f)
}

func TestProviderUpdateConsecutiveLinkedArchives(t *testing.T) {
	for _, linkType := range []byte{tar.TypeSymlink, tar.TypeLink} {
		t.Run(
			map[byte]string{tar.TypeSymlink: "symlink", tar.TypeLink: "hardlink"}[linkType],
			func(t *testing.T) {
				if linkType == tar.TypeSymlink {
					requireProviderUpdateSymlinks(t)
				}
				f := newPreservationFixture(t)
				f.seedBinary("UNRELATED", []byte("unrelated installed bytes"))
				first, second := []byte("first linked release"), []byte("second linked release")
				const stale = "obsolete-release-member"
				archives := [][]byte{
					linkedReplacementArchive(t, linkType, first, stale),
					linkedReplacementArchive(t, linkType, second, ""),
				}
				var requests atomic.Int64
				server := ownedProviderUpdateServer(
					t,
					func(w http.ResponseWriter, _ *http.Request) {
						index := requests.Add(1) - 1
						if index >= int64(len(archives)) {
							http.Error(w, "unexpected additional request", http.StatusBadRequest)
							return
						}
						_, _ = w.Write(archives[index])
					},
				)
				for _, content := range [][]byte{first, second} {
					binary := updateTestBinary(server.URL+"/linked.tar", content)
					binary.ArchivePath = updateRuntimeName
					err := f.update(
						map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}},
					)
					t.Logf("consecutive archive request=%d result=%v", requests.Load(), err)
					require.NoError(t, err)
					assertLinkedReplacement(t, f, linkType, content)
				}
				require.EqualValues(
					t,
					2,
					requests.Load(),
					"changed checksum must fetch a new archive",
				)
				require.NoFileExists(t, filepath.Join(f.binaries, updateRuntimeName, stale))
			},
		)
	}
}

func truncatedLinkedArchive(t *testing.T, linkType byte, content []byte) []byte {
	t.Helper()
	const partial = "attempt-only-member"
	var data bytes.Buffer
	writer := tar.NewWriter(&data)
	for _, header := range []*tar.Header{
		{Name: updateRuntimeName, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(content))},
		{Name: updateArchiveLink, Typeflag: linkType, Linkname: updateRuntimeName, Mode: 0o777},
		{Name: partial, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))},
		{Name: "incomplete", Typeflag: tar.TypeReg, Mode: 0o644, Size: 128},
	} {
		require.NoError(t, writer.WriteHeader(header))
		if header.Name == "incomplete" {
			_, err := writer.Write([]byte("short"))
			require.NoError(t, err)
			break
		}
		if header.Typeflag == tar.TypeReg {
			_, err := writer.Write(content)
			require.NoError(t, err)
		}
	}
	// Deliberately truncated member follows a completed link; this is a retriable tar read error.
	require.Error(t, writer.Close())
	return data.Bytes()
}

func TestProviderUpdateRetriesLinkedArchiveFromFreshFamily(t *testing.T) {
	for _, linkType := range []byte{tar.TypeSymlink, tar.TypeLink} {
		t.Run(
			map[byte]string{tar.TypeSymlink: "symlink", tar.TypeLink: "hardlink"}[linkType],
			func(t *testing.T) {
				if linkType == tar.TypeSymlink {
					requireProviderUpdateSymlinks(t)
				}
				f := newPreservationFixture(t)
				f.seedBinary(updateRuntimeKey, []byte("old live runtime"))
				f.seedBinary("UNRELATED", []byte("unrelated installed bytes"))
				content := []byte("complete archive after retry")
				first := truncatedLinkedArchive(t, linkType, content)
				second := linkedReplacementArchive(t, linkType, content, "")
				var requests atomic.Int64
				server := ownedProviderUpdateServer(
					t,
					func(w http.ResponseWriter, _ *http.Request) {
						if requests.Add(1) == 1 {
							_, _ = w.Write(first)
							return
						}
						_, _ = w.Write(second)
					},
				)
				binary := updateTestBinary(server.URL+"/retry.tar", content)
				binary.ArchivePath = updateRuntimeName
				err := f.update(map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}})
				t.Logf("linked archive retry requests=%d result=%v", requests.Load(), err)
				require.EqualValues(t, 2, requests.Load())
				require.NoError(t, err)
				assertLinkedReplacement(t, f, linkType, content)
				require.NoFileExists(
					t,
					filepath.Join(f.binaries, updateRuntimeName, "attempt-only-member"),
				)
				require.NoFileExists(t, filepath.Join(f.binaries, updateRuntimeName, "incomplete"))
			},
		)
	}
}

func TestProviderUpdateFamilyAliasPreservesUnrelatedBinary(t *testing.T) {
	for _, cache := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "fresh archive", true: "offline executable cache"}[cache],
			func(t *testing.T) {
				requireProviderUpdateSymlinks(t)
				f := newPreservationFixture(t)
				old := []byte("unrelated executable must not change")
				f.seedBinary("UNRELATED", old)
				alias := filepath.Join(f.binaries, updateRuntimeName)
				require.NoError(t, os.Symlink("unrelated", alias))
				content := []byte("replacement through alias " + f.dir)
				archive := singleExecutableUpdateArchive(t, updateRuntimeName, content)
				var requests atomic.Int64
				server := ownedProviderUpdateServer(
					t,
					func(w http.ResponseWriter, _ *http.Request) {
						requests.Add(1)
						_, _ = w.Write(archive)
					},
				)
				binary := updateTestBinary(server.URL+"/family-alias.tar", content)
				binary.ArchivePath = updateRuntimeName
				binaries := map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}}
				if cache {
					warmProviderUpdateCache(t, binaries, server)
				}
				require.NoError(t, f.update(binaries))
				require.EqualValues(
					t,
					1,
					requests.Load(),
					"offline cache case must not fetch again",
				)
				assertSelectedExecutable(
					t,
					f.binaryPath(updateRuntimeKey),
					content,
					binary.Checksum,
				)
				actual, err := os.ReadFile(f.binaryPath("UNRELATED"))
				t.Logf(
					"unrelated binary after successful replacement: read error=%v bytes=%q",
					err,
					actual,
				)
				require.NoError(
					t,
					err,
					"updating an alias family must preserve the original target family",
				)
				assert.Equal(
					t,
					old,
					actual,
					"cache preparation must not overwrite another family's executable",
				)
				assertNoUpdateStaging(t, f)
			},
		)
	}
}

func warmProviderUpdateCache(
	t *testing.T,
	binaries map[string][]*provider.ProviderBinary,
	server *httptest.Server,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := provider.DownloadBinaries(ctx, binaries, t.TempDir())
	require.NoError(t, err)
	server.Close()
}

func TestProviderUpdateFamilyAliasMatchingChecksumReuse(t *testing.T) {
	requireProviderUpdateSymlinks(t)
	f := newPreservationFixture(t)
	content := []byte("valid installed executable behind a contained family alias")
	f.seedBinary("UNRELATED", content)
	alias := filepath.Join(f.binaries, updateRuntimeName)
	require.NoError(t, os.Symlink("unrelated", alias))
	binary := updateTestBinary("https://offline.invalid/matching-family.tar", content)
	binary.ArchivePath = updateRuntimeName
	require.NoError(t, f.update(map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}}))
	assertSelectedExecutable(t, f.binaryPath(updateRuntimeKey), content, binary.Checksum)
	assertSelectedExecutable(t, f.binaryPath("UNRELATED"), content, binary.Checksum)
	target, err := os.Readlink(alias)
	require.NoError(t, err)
	assert.Equal(t, "unrelated", target, "matching installed reuse should not reset the family")
	assertNoUpdateStaging(t, f)
}

func TestProviderUpdateReadOnlySelectedArchiveFamily(t *testing.T) {
	if runtime.GOOS == updateWindowsOS || os.Geteuid() == 0 {
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
				archive := singleExecutableUpdateArchive(t, updateRuntimeName, content)
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

type updateAliasFixture struct {
	fixture   *preservationFixture
	selected  string
	preserved map[string]os.FileMode
}

func newUpdateAliasFixture(t *testing.T, kind string) updateAliasFixture {
	t.Helper()
	requireProviderUpdateSymlinks(t)
	f := newPreservationFixture(t)
	f.seedBinary("UNRELATED", []byte("unrelated executable retained through alias replacement"))
	unrelatedSidecar := filepath.Join(f.binaries, "unrelated", "sidecar")
	f.put(unrelatedSidecar, []byte("unrelated ancillary content"))
	family := filepath.Join(f.binaries, updateRuntimeName)
	selected := updateRuntimeName
	if kind == "family" {
		require.NoError(t, os.Symlink("unrelated", family))
	} else {
		require.NoError(t, os.MkdirAll(family, 0o700))
		if kind == "nested" {
			selected = "bin/runtime"
			require.NoError(t, os.Symlink("../unrelated", filepath.Join(family, "bin")))
		} else {
			require.Equal(t, "leaf", kind)
			require.NoError(
				t,
				os.Symlink("../unrelated/runtime", filepath.Join(family, updateRuntimeName)),
			)
		}
		f.put(filepath.Join(family, "sidecar"), []byte("selected family ancillary content"))
	}
	preserved := map[string]os.FileMode{}
	for _, path := range []string{f.binaryPath("UNRELATED"), unrelatedSidecar, filepath.Join(family, "sidecar")} {
		info, err := os.Stat(path)
		require.NoError(t, err)
		preserved[path] = info.Mode().Perm()
		if _, ok := f.before[path]; !ok {
			f.before[path], err = os.ReadFile(
				path,
			) // #nosec G304 -- Snapshot of an owned fixture alias.
			require.NoError(t, err)
		}
	}
	return updateAliasFixture{fixture: f, selected: selected, preserved: preserved}
}

func (a updateAliasFixture) assertReplacement(
	t *testing.T,
	binary *provider.ProviderBinary,
	content []byte,
) {
	t.Helper()
	selected := filepath.Join(a.fixture.binaries, updateRuntimeName, filepath.FromSlash(a.selected))
	assertSelectedExecutable(t, selected, content, binary.Checksum)
	for path, mode := range a.preserved {
		actual, err := os.ReadFile(
			path,
		) // #nosec G304 -- All paths belong to this test-owned provider tree.
		require.NoError(t, err)
		assert.Equal(t, a.fixture.before[path], actual, path)
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, mode, info.Mode().Perm(), path)
	}
	a.fixture.assertAssociation()
	assertNoUpdateStaging(t, a.fixture)
}

func TestProviderUpdateNestedAndLeafAliasOfflineCache(t *testing.T) {
	for _, kind := range []string{"nested", "leaf"} {
		t.Run(kind, func(t *testing.T) {
			a := newUpdateAliasFixture(t, kind)
			content := []byte("cached independent replacement " + a.fixture.dir)
			archive := compatibilityTar(t, false, []*tar.Header{
				{Name: a.selected, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(content))},
			}, map[string][]byte{a.selected: content})
			var requests atomic.Int64
			server := ownedProviderUpdateServer(t, func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_, _ = w.Write(archive)
			})
			binary := updateTestBinary(server.URL+"/nested-alias.tar", content)
			binary.ArchivePath = a.selected
			binaries := map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := provider.DownloadBinaries(ctx, binaries, t.TempDir())
			require.NoError(t, err)
			server.Close()
			require.NoError(
				t,
				a.fixture.update(binaries),
				"warmed executable cache must work with the source offline",
			)
			require.EqualValues(
				t,
				1,
				requests.Load(),
				"replacement must not fetch the archive again",
			)
			a.assertReplacement(t, binary, content)
		})
	}
}

func TestProviderUpdatePlainAndLocalAncestorAlias(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "plain remote", true: "relative local"}[local],
			func(t *testing.T) {
				a := newUpdateAliasFixture(t, "family")
				content := []byte("independent plain replacement " + a.fixture.dir)
				binary := updateTestBinary("source-runtime", content)
				var requests atomic.Int64
				if local {
					t.Chdir(t.TempDir())
					require.NoError(t, os.WriteFile(binary.Path, content, 0o600))
				} else {
					server := ownedProviderUpdateServer(
						t,
						func(w http.ResponseWriter, _ *http.Request) {
							requests.Add(1)
							_, _ = w.Write(content)
						},
					)
					binary.Path = server.URL + "/plain-runtime"
				}
				require.NoError(
					t,
					a.fixture.update(
						map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}},
					),
				)
				if local {
					actual, err := os.ReadFile(
						binary.Path,
					) // #nosec G304 -- Relative source in owned temporary cwd.
					require.NoError(t, err)
					assert.Equal(t, content, actual)
				} else {
					require.EqualValues(t, 1, requests.Load())
				}
				a.assertReplacement(t, binary, content)
			},
		)
	}
}

func TestProviderUpdateAncestorRootAliasOfflineCache(t *testing.T) {
	requireProviderUpdateSymlinks(t)
	f := newPreservationFixture(t)
	old := []byte("original root descendant remains unchanged")
	f.seedBinary("UNRELATED", old)
	f.put(
		filepath.Join(f.binaries, "unrelated", "sidecar"),
		[]byte("retained root descendant sidecar"),
	)
	require.NoError(t, os.Symlink(".", filepath.Join(f.binaries, updateRuntimeName)))
	require.NoError(t, os.Symlink("unrelated", filepath.Join(f.binaries, "directory-link")))
	require.NoError(t, os.Symlink("unrelated/runtime", filepath.Join(f.binaries, "file-link")))
	content := []byte("root alias cached replacement " + f.dir)
	selected := "unrelated/runtime"
	archive := compatibilityTar(t, false, []*tar.Header{
		{Name: selected, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(content))},
	}, map[string][]byte{selected: content})
	var requests atomic.Int64
	server := ownedProviderUpdateServer(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(archive)
	})
	binary := updateTestBinary(server.URL+"/ancestor-root.tar", content)
	binary.ArchivePath = selected
	binaries := map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := provider.DownloadBinaries(ctx, binaries, t.TempDir())
	require.NoError(t, err)
	server.Close()
	require.NoError(
		t,
		f.update(binaries),
		"ancestor-root cloning must complete within the fixture deadline",
	)
	require.EqualValues(t, 1, requests.Load(), "offline consumption must not refetch")
	family := filepath.Join(f.binaries, updateRuntimeName)
	assertSelectedExecutable(
		t,
		filepath.Join(family, filepath.FromSlash(selected)),
		content,
		binary.Checksum,
	)
	assertSelectedExecutable(t, f.binaryPath("UNRELATED"), old, updateTestDigest(old))
	assertRebasedUpdateRootLinks(t, f, family, old)
	assertNoUpdateStaging(t, f)
}

func assertRebasedUpdateRootLinks(t *testing.T, f *preservationFixture, family string, old []byte) {
	t.Helper()
	for _, link := range []string{"directory-link", "file-link"} {
		path := filepath.Join(family, link)
		info, err := os.Lstat(path)
		require.NoError(t, err)
		require.NotZero(t, info.Mode()&os.ModeSymlink)
		resolved, err := filepath.EvalSymlinks(path)
		require.NoError(t, err)
		original, err := filepath.EvalSymlinks(filepath.Join(f.binaries, link))
		require.NoError(t, err)
		assert.Equal(
			t,
			original,
			resolved,
			"cloned link must keep the original target, not the replaced executable",
		)
	}
	for _, path := range []string{
		filepath.Join(family, "file-link"), filepath.Join(family, "directory-link", updateRuntimeName),
	} {
		assertSelectedExecutable(t, path, old, updateTestDigest(old))
	}
	for _, base := range []string{f.binaries, family} {
		path := filepath.Join(base, "unrelated", "sidecar")
		actual, err := os.ReadFile(path) // #nosec G304 -- Original or cloned test-owned sidecar.
		require.NoError(t, err)
		assert.Equal(t, f.before[filepath.Join(f.binaries, "unrelated", "sidecar")], actual)
	}
	entries, err := os.ReadDir(family)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotContains(
			t,
			entry.Name(),
			".update-destination-",
			"temporary clone must not copy itself",
		)
	}
	f.assertAssociation()
}

func TestProviderUpdateReadOnlyNestedSelectedExecutable(t *testing.T) {
	if runtime.GOOS == updateWindowsOS || os.Geteuid() == 0 {
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

func TestProviderUpdateConflictingReusedFamilyAlias(t *testing.T) {
	requireProviderUpdateSymlinks(t)
	f := newPreservationFixture(t)
	old := []byte("both selected families initially expose these old bytes")
	f.seedBinary(updateFirstKey, old)
	f.seedBinary(updateSecondKey, old)
	firstFamily := filepath.Dir(f.binaryPath(updateFirstKey))
	require.NoError(t, os.Remove(f.binaryPath(updateFirstKey)))
	require.NoError(t, os.Remove(firstFamily))
	target := filepath.Base(filepath.Dir(f.binaryPath(updateSecondKey)))
	require.NoError(t, os.Symlink(target, firstFamily))
	content := []byte("only the second family requests these new bytes")
	var missingRequests, replacementRequests atomic.Int64
	server := ownedProviderUpdateServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing-first" {
			missingRequests.Add(1)
			http.Error(w, "owned first-family source deliberately unavailable", http.StatusNotFound)
			return
		}
		replacementRequests.Add(1)
		_, _ = w.Write(content)
	})
	first := updateTestBinary(server.URL+"/missing-first", old)
	second := updateTestBinary(server.URL+"/new-second", content)
	err := f.update(map[string][]*provider.ProviderBinary{
		updateFirstKey: {first}, updateSecondKey: {second},
	})
	require.EqualValues(t, 1, replacementRequests.Load())
	t.Logf("first-family fetches=%d; update result=%v", missingRequests.Load(), err)
	if err == nil {
		// Independent materialization is permitted, but alias reuse must never
		// publish the second family's bytes under the first family's checksum.
		assertSelectedExecutable(t, f.binaryPath(updateFirstKey), old, first.Checksum)
		assertSelectedExecutable(t, f.binaryPath(updateSecondKey), content, second.Checksum)
	} else {
		require.True(
			t,
			strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "checksum"),
			"both map iteration orders must fail for the conflict, not an unrelated error: %v",
			err,
		)
		f.assertFailedPreservation()
		link, linkErr := os.Readlink(firstFamily)
		require.NoError(t, linkErr)
		assert.Equal(t, target, link)
	}
	assertNoUpdateStaging(t, f)
}

func TestProviderUpdateEmptyBinaryKeyPreservesUnrelatedFamily(t *testing.T) {
	f := newPreservationFixture(t)
	content := []byte("empty-key selected executable")
	f.seedBinary("UNRELATED", []byte("retained unrelated installed executable"))
	archive := compatibilityTar(t, false, []*tar.Header{
		{Name: updateRuntimeName, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(content))},
	}, map[string][]byte{updateRuntimeName: content})
	server := compatibilityArchiveServer(t, archive)
	binary := updateTestBinary(server.URL+"/empty-key.tar", content)
	binary.ArchivePath = updateRuntimeName

	err := f.update(map[string][]*provider.ProviderBinary{"": {binary}})
	require.ErrorContains(t, err, "unsafe provider update binary key")
	f.assertFailedPreservation()
	assertNoUpdateStaging(t, f)
}
