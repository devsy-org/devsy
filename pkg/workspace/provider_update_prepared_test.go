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
