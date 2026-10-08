package extract

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/moby/patternmatcher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	readmeFile      = "README.md"
	emptyFolder     = "empty"
	worktrees       = ".claude/worktrees/"
	nodeModules     = "**/node_modules/"
	buildDirectory  = "build/"
	deepKeepFile    = "build/deep/keep.txt"
	keepBuildFile   = "build/keep.txt"
	removeBuildFile = "build/remove.txt"
	buildSourceFile = "buildSrc/main.go"
	debugLogFile    = "debug.log"
	workerLogFile   = "src/worker.log"
	docsNotesFile   = "docs/notes.md"
	windowsOS       = "windows"
	ancestorSymlink = "ancestor symlink"
)

var testTree = []string{
	readmeFile,
	".claude/settings.json",
	".claude/worktrees/feature/src/main.go",
	".claude/worktrees/feature/keep.txt",
	"node_modules/lib/index.js",
	"web/node_modules/lib/index.js",
	"web/app/node_modules/lib/index.js",
	"web/app/main.js",
	"build/out.bin",
	"core/build/out.bin",
	"buildSrc/plugin.kt",
	"docs/guide.tgz",
	"docs/guide.md",
	"docs/old/archive.tgz",
}

func newTestTree(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	for _, file := range testTree {
		p := filepath.Join(root, filepath.FromSlash(file))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		require.NoError(t, os.WriteFile(p, []byte(file), 0o600))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, emptyFolder), 0o750))

	return root
}

func tarEntries(t *testing.T, root string, excludes []string) []string {
	t.Helper()

	buf := &bytes.Buffer{}
	require.NoError(t, WriteTarExclude(buf, root, false, excludes))

	entries := []string{}
	reader := tar.NewReader(buf)
	for {
		hdr, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		entries = append(entries, hdr.Name)
	}
	sort.Strings(entries)

	return entries
}

func allEntries() []string {
	entries := append([]string{emptyFolder}, testTree...)
	sort.Strings(entries)
	return entries
}

func without(entries []string, excluded ...string) []string {
	result := []string{}
	for _, entry := range entries {
		if !slices.Contains(excluded, entry) {
			result = append(result, entry)
		}
	}
	return result
}

type writeTarExcludeTest struct {
	name     string
	excludes []string
	expected []string
}

var writeTarExcludeTests = []writeTarExcludeTest{
	{
		name:     "nil excludes archive everything",
		excludes: nil,
		expected: allEntries(),
	},
	{
		name:     "empty excludes archive everything",
		excludes: []string{},
		expected: allEntries(),
	},
	{
		name:     "directory pattern excludes the directory and its contents",
		excludes: []string{worktrees},
		expected: without(allEntries(),
			".claude/worktrees/feature/src/main.go",
			".claude/worktrees/feature/keep.txt",
		),
	},
	{
		name:     "double star excludes a directory at any depth",
		excludes: []string{nodeModules},
		expected: without(allEntries(),
			"node_modules/lib/index.js",
			"web/node_modules/lib/index.js",
			"web/app/node_modules/lib/index.js",
		),
	},
	{
		name:     "single star excludes only matching files",
		excludes: []string{"docs/*.tgz"},
		expected: without(allEntries(), "docs/guide.tgz"),
	},
	{
		name:     "a name does not exclude a longer name it is a prefix of",
		excludes: []string{"build"},
		expected: without(allEntries(), "build/out.bin"),
	},
	{
		name:     "double star build excludes nested build folders only",
		excludes: []string{"**/build/"},
		expected: without(allEntries(), "build/out.bin", "core/build/out.bin"),
	},
	{
		name:     "negation re-includes a file inside an excluded directory",
		excludes: []string{worktrees, "!.claude/worktrees/feature/keep.txt"},
		expected: without(allEntries(), ".claude/worktrees/feature/src/main.go"),
	},
	{
		name:     "negation with double star re-includes a file inside an excluded directory",
		excludes: []string{worktrees, "!**/keep.txt"},
		expected: without(allEntries(), ".claude/worktrees/feature/src/main.go"),
	},
	{
		name:     "excluded empty directory is not archived",
		excludes: []string{emptyFolder, "!README.md"},
		expected: without(allEntries(), emptyFolder),
	},
	{
		name: "ignore file of the identity server",
		excludes: []string{
			".claude/worktrees", ".gradle", "**/build", "**/node_modules", "dist",
			"tools/chrome", "tools/firefox", "docs/*.tgz",
		},
		expected: []string{
			".claude/settings.json",
			readmeFile,
			"buildSrc/plugin.kt",
			"docs/guide.md",
			"docs/old/archive.tgz",
			emptyFolder,
			"web/app/main.js",
		},
	},
}

func TestWriteTarExclude(t *testing.T) {
	for _, tt := range writeTarExcludeTests {
		t.Run(tt.name, func(t *testing.T) {
			root := newTestTree(t)
			assert.Equal(t, tt.expected, tarEntries(t, root, tt.excludes))
		})
	}
}

func TestWriteTarExcludeSingleFile(t *testing.T) {
	root := newTestTree(t)

	assert.Equal(
		t,
		[]string{readmeFile},
		tarEntries(t, filepath.Join(root, readmeFile), []string{"docs"}),
	)
	assert.Empty(t, tarEntries(t, filepath.Join(root, readmeFile), []string{readmeFile}))
}

func TestArchiverMayReincludeBelow(t *testing.T) {
	archiver, err := NewArchiver(t.TempDir(), nil, []string{
		worktrees, nodeModules, "!.claude/worktrees/feature/keep.txt",
	})
	require.NoError(t, err)

	assert.True(t, archiver.mayReincludeBelow(".claude/worktrees"))
	assert.False(t, archiver.mayReincludeBelow("web/node_modules"))
}

func TestPatternMayMatchBelow(t *testing.T) {
	const keep = "a/b/keep.txt"
	const ab = "a/b"
	tests := []struct {
		pattern  string
		dir      string
		expected bool
	}{
		{pattern: keep, dir: "a", expected: true},
		{pattern: keep, dir: ab, expected: true},
		{pattern: "a/*/keep.txt", dir: ab, expected: true},
		{pattern: "**/keep.txt", dir: "x/y", expected: true},
		{pattern: "a/**", dir: "z", expected: true},
		{pattern: "a/[b/keep.txt", dir: "a/c", expected: true},
		{pattern: keep, dir: "c", expected: false},
		{pattern: keep, dir: "a/c", expected: false},
		{pattern: ab, dir: ab, expected: false},
		{pattern: "keep.txt", dir: "a", expected: false},
		{pattern: keep, dir: keep, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+" below "+tt.dir, func(t *testing.T) {
			assert.Equal(t, tt.expected, patternMayMatchBelow(
				strings.Split(tt.pattern, "/"), strings.Split(tt.dir, "/")))
		})
	}
}

func TestWriteTarWithOptionsProtectsExactFolders(t *testing.T) {
	root := newTestTree(t)

	buf := &bytes.Buffer{}
	require.NoError(t, WriteTarWithOptions(buf, root, TarOptions{
		Excludes:       []string{nodeModules, "web/"},
		ProtectedPaths: []string{"web/app/node_modules"},
	}))

	entries := []string{}
	reader := tar.NewReader(buf)
	for {
		hdr, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		entries = append(entries, hdr.Name)
	}

	assert.NotContains(t, entries, "node_modules/lib/index.js")
	assert.NotContains(t, entries, "web/node_modules/lib/index.js")
	assert.Contains(t, entries, "web/app/node_modules/lib/index.js")
}

func TestWriteTarExcludeInvalidPattern(t *testing.T) {
	root := newTestTree(t)

	var buf bytes.Buffer
	err := WriteTarExclude(&buf, root, true, []string{"[a-"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exclude patterns")
	assert.Zero(t, buf.Len())
}

var orderedPatternFiles = []string{
	keepBuildFile,
	removeBuildFile,
	deepKeepFile,
	buildSourceFile,
	debugLogFile,
	workerLogFile,
	docsNotesFile,
}

var orderedPatternTests = []struct {
	patterns []string
	included []string
}{
	{
		[]string{buildDirectory},
		[]string{buildSourceFile, debugLogFile, workerLogFile, docsNotesFile},
	},
	{
		[]string{"*.log"},
		[]string{
			keepBuildFile,
			removeBuildFile,
			deepKeepFile,
			buildSourceFile,
			workerLogFile,
			docsNotesFile,
		},
	},
	{
		[]string{"**/*.log"},
		[]string{
			keepBuildFile,
			removeBuildFile,
			deepKeepFile,
			buildSourceFile,
			docsNotesFile,
		},
	},
	{
		[]string{buildDirectory, "!build/keep.txt"},
		[]string{
			keepBuildFile,
			buildSourceFile,
			debugLogFile,
			workerLogFile,
			docsNotesFile,
		},
	},
	{
		[]string{buildDirectory, "!build/**/keep.txt", "build/deep/"},
		[]string{
			keepBuildFile,
			buildSourceFile,
			debugLogFile,
			workerLogFile,
			docsNotesFile,
		},
	},
	{
		[]string{buildDirectory, "!buil?/de?p/[k]eep.txt"},
		[]string{
			deepKeepFile,
			buildSourceFile,
			debugLogFile,
			workerLogFile,
			docsNotesFile,
		},
	},
	{[]string{"build/deep/", "!build/"}, orderedPatternFiles},
	{
		[]string{buildDirectory, "!build/", removeBuildFile},
		[]string{
			keepBuildFile,
			deepKeepFile,
			buildSourceFile,
			debugLogFile,
			workerLogFile,
			docsNotesFile,
		},
	},
	{
		[]string{buildDirectory, "!build**/deep/keep.txt"},
		[]string{
			deepKeepFile,
			buildSourceFile,
			debugLogFile,
			workerLogFile,
			docsNotesFile,
		},
	},
}

func TestWriteTarOrderedPatterns(t *testing.T) {
	root := t.TempDir()
	for _, file := range orderedPatternFiles {
		name := filepath.Join(root, filepath.FromSlash(file))
		require.NoError(t, os.MkdirAll(filepath.Dir(name), 0o750))
		require.NoError(t, os.WriteFile(name, []byte(file), 0o600))
	}
	for _, tt := range orderedPatternTests {
		t.Run(strings.Join(tt.patterns, ","), func(t *testing.T) {
			assert.ElementsMatch(t, tt.included, tarEntries(t, root, tt.patterns))
		})
	}
}

func TestWriteTarRejectsInvalidProtectedPathsBeforeWriting(t *testing.T) {
	root := newTestTree(t)
	invalidPaths := []string{
		"", ".", "..", "../README.md", "/README.md", "web/../README.md",
		"web//app", `web\app`, "C:/README.md", "missing",
	}
	for _, protected := range invalidPaths {
		t.Run(protected, func(t *testing.T) {
			var buf bytes.Buffer
			err := WriteTarWithOptions(
				&buf,
				root,
				TarOptions{Compress: true, ProtectedPaths: []string{protected}},
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "protected path")
			assert.Zero(t, buf.Len())
		})
	}
}

func TestWriteTarProtectedPathsRejectSymlinkComponents(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600))
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, protected := range []string{"link", "link/secret.txt"} {
		var buf bytes.Buffer
		err := WriteTarWithOptions(&buf, root, TarOptions{ProtectedPaths: []string{protected}})
		require.ErrorContains(t, err, "symbolic link")
		assert.Zero(t, buf.Len())
	}
	var buf bytes.Buffer
	require.NoError(t, WriteTar(&buf, root, false))
	reader := tar.NewReader(&buf)
	header, err := reader.Next()
	require.NoError(t, err)
	assert.Equal(t, "link", header.Name)
	assert.Equal(t, byte(tar.TypeSymlink), header.Typeflag)
	assert.Equal(t, outside, header.Linkname)
	_, err = reader.Next()
	require.ErrorIs(t, err, io.EOF)
}

func TestWriteTarProtectedFileDoesNotProtectSiblings(t *testing.T) {
	root := newTestTree(t)
	var buf bytes.Buffer
	require.NoError(
		t,
		WriteTarWithOptions(
			&buf,
			root,
			TarOptions{
				Excludes:       []string{"**"},
				ProtectedPaths: []string{".claude/worktrees/feature/keep.txt"},
			},
		),
	)
	reader := tar.NewReader(&buf)
	header, err := reader.Next()
	require.NoError(t, err)
	assert.Equal(t, ".claude/worktrees/feature/keep.txt", header.Name)
	_, err = reader.Next()
	require.ErrorIs(t, err, io.EOF)
}

func TestWriteTarWithCompiledMatcher(t *testing.T) {
	root := newTestTree(t)
	matcher, err := patternmatcher.New([]string{"**"})
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(
		t,
		WriteTarWithOptions(
			&buf,
			root,
			TarOptions{Matcher: matcher, Excludes: []string{"[invalid"}},
		),
	)
	_, err = tar.NewReader(&buf).Next()
	require.ErrorIs(t, err, io.EOF)
}

type failingArchiveWriter struct {
	err       error
	remaining int
}

func (w *failingArchiveWriter) Write(p []byte) (int, error) {
	if len(p) <= w.remaining {
		w.remaining -= len(p)
		return len(p), nil
	}
	return 0, w.err
}

func TestWriteTarReturnsCloseFailures(t *testing.T) {
	writeErr := errors.New("archive destination failed")
	for _, compress := range []bool{false, true} {
		writer := &failingArchiveWriter{err: writeErr}
		if compress {
			// Accept the gzip header so the failure occurs during its final flush.
			writer.remaining = 10
		}
		err := WriteTar(writer, t.TempDir(), compress)
		require.ErrorIs(t, err, writeErr)
	}
}

func TestWriteTarGzipContents(t *testing.T) {
	root := newTestTree(t)
	var buf bytes.Buffer
	require.NoError(t, WriteTarExclude(&buf, root, true, []string{"**", "!README.md"}))
	reader, err := gzip.NewReader(&buf)
	require.NoError(t, err)
	defer func() { require.NoError(t, reader.Close()) }()
	tarReader := tar.NewReader(reader)
	header, err := tarReader.Next()
	require.NoError(t, err)
	assert.Equal(t, readmeFile, header.Name)
	body, err := io.ReadAll(tarReader)
	require.NoError(t, err)
	assert.Equal(t, readmeFile, string(body))
	_, err = tarReader.Next()
	require.ErrorIs(t, err, io.EOF)
	//nolint:gosec // The gzip source was generated from this bounded fixture above.
	_, err = io.Copy(io.Discard, reader)
	require.NoError(t, err)
}

func TestArchiverRejectsNonlocalEntryPaths(t *testing.T) {
	archiver, err := NewArchiver(t.TempDir(), tar.NewWriter(io.Discard), nil)
	require.NoError(t, err)
	for _, name := range []string{"../outside", "/absolute", "a/../outside"} {
		require.Error(t, archiver.AddToArchive(name))
	}
}

func TestWriteTarPrunesUnreadableExcludedDirectory(t *testing.T) {
	if runtime.GOOS == windowsOS {
		t.Skip("POSIX directory permissions required")
	}
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	require.NoError(t, os.Mkdir(blocked, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(blocked, "keep.txt"), []byte("keep"), 0o600))
	require.NoError(t, os.Chmod(blocked, 0))
	//nolint:gosec // Restore access to this temporary directory for cleanup.
	t.Cleanup(func() { require.NoError(t, os.Chmod(blocked, 0o750)) })
	if _, err := os.ReadDir(blocked); err == nil {
		t.Skip("current user can bypass directory permissions")
	}
	assert.Empty(t, tarEntries(t, root, []string{"blocked/"}))
	err := WriteTarExclude(io.Discard, root, false, []string{"blocked/", "!blocked/keep.txt"})
	require.ErrorIs(t, err, os.ErrPermission)
	assert.Contains(t, err.Error(), "read archive directory")
}

func TestWriteTarReturnsUnreadableFileError(t *testing.T) {
	if runtime.GOOS == windowsOS {
		t.Skip("POSIX file permissions required")
	}
	root := t.TempDir()
	name := filepath.Join(root, "unreadable")
	require.NoError(t, os.WriteFile(name, []byte("payload"), 0))
	t.Cleanup(func() { require.NoError(t, os.Chmod(name, 0o600)) })
	// #nosec G304 -- name is a temporary fixture created immediately above.
	file, err := os.Open(name)
	if err == nil {
		require.NoError(t, file.Close())
		t.Skip("current user can bypass file permissions")
	}
	err = WriteTar(io.Discard, root, false)
	require.ErrorIs(t, err, os.ErrPermission)
	assert.Contains(t, err.Error(), "open archive file")
}

func TestArchiverReturnsMissingPathError(t *testing.T) {
	archiver, err := NewArchiver(t.TempDir(), tar.NewWriter(io.Discard), nil)
	require.NoError(t, err)
	require.ErrorIs(t, archiver.AddToArchive("missing"), os.ErrNotExist)
}

type mutatingArchiveWriter struct {
	bytes.Buffer
	beforeFirstWrite func()
}

func (w *mutatingArchiveWriter) Write(p []byte) (int, error) {
	if w.beforeFirstWrite != nil {
		mutate := w.beforeFirstWrite
		w.beforeFirstWrite = nil
		mutate()
	}
	return w.Buffer.Write(p)
}

func TestWriteTarProtectedFilesUseApprovedBytes(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("first"), 0o600))
	artifact := filepath.Join(root, "z", "artifact")
	require.NoError(t, os.MkdirAll(filepath.Dir(artifact), 0o750))
	approved := []byte("approved generated contents")
	require.NoError(t, os.WriteFile(artifact, approved, 0o600))
	writer := &mutatingArchiveWriter{beforeFirstWrite: func() {
		require.NoError(
			t,
			os.WriteFile(artifact, []byte("changed ignored contents with another size"), 0o600),
		)
	}}
	require.NoError(t, WriteTarWithOptions(writer, root, TarOptions{
		Excludes: []string{
			"z/",
		},
		ProtectedFiles: map[string]ProtectedFile{
			"z/artifact": {Reader: bytes.NewReader(approved), Size: int64(len(approved))},
		},
	}))
	reader := tar.NewReader(&writer.Buffer)
	_, err := reader.Next()
	require.NoError(t, err)
	header, err := reader.Next()
	require.NoError(t, err)
	assert.Equal(t, "z/artifact", header.Name)
	assert.Equal(t, byte(tar.TypeReg), header.Typeflag)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, approved, body)
}

func TestWriteTarProtectedFileMutationCannotOmitArtifact(t *testing.T) {
	for _, mutation := range []string{"missing", "directory", ancestorSymlink} {
		t.Run(mutation, func(t *testing.T) {
			if mutation == ancestorSymlink && runtime.GOOS == windowsOS {
				t.Skip("symlink creation requires privileges")
			}
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("first"), 0o600))
			artifact := filepath.Join(root, "z", "artifact")
			require.NoError(t, os.MkdirAll(filepath.Dir(artifact), 0o750))
			require.NoError(t, os.WriteFile(artifact, []byte("approved"), 0o600))
			writer := &mutatingArchiveWriter{beforeFirstWrite: func() {
				require.NoError(t, os.Remove(artifact))
				switch mutation {
				case "directory":
					require.NoError(t, os.Mkdir(artifact, 0o750))
				case ancestorSymlink:
					require.NoError(t, os.Remove(filepath.Dir(artifact)))
					require.NoError(t, os.Symlink(t.TempDir(), filepath.Dir(artifact)))
				}
			}}
			err := WriteTarWithOptions(
				writer,
				root,
				TarOptions{
					Excludes: []string{"z/"},
					ProtectedFiles: map[string]ProtectedFile{
						"z/artifact": {Reader: strings.NewReader("approved"), Size: 8},
					},
				},
			)
			require.Error(t, err)
		})
	}
}

type archiveStatusReader struct{ err error }

func (r archiveStatusReader) Read([]byte) (int, error) { return 0, r.err }

func TestWriteTarTraversalFailureCannotHideTransportError(t *testing.T) {
	for _, tt := range []struct {
		name     string
		compress bool
	}{{name: "tar"}, {name: "gzip", compress: true}} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(
				t,
				os.WriteFile(filepath.Join(root, "early.txt"), []byte("earlier content"), 0o600),
			)
			lateFile := filepath.Join(root, "late.txt")
			require.NoError(t, os.WriteFile(lateFile, []byte("later content"), 0o600))
			writer := &mutatingArchiveWriter{beforeFirstWrite: func() {
				require.NoError(t, os.Remove(lateFile))
			}}
			err := WriteTar(writer, root, tt.compress)
			require.ErrorIs(t, err, os.ErrNotExist)
			transportErr := errors.New("remote source read failed")
			stream := io.MultiReader(
				bytes.NewReader(writer.Bytes()),
				archiveStatusReader{err: transportErr},
			)
			require.ErrorIs(t, Extract(stream, t.TempDir()), transportErr)
		})
	}
}
