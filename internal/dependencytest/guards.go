package dependencytest

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func Environment() []string {
	env := os.Environ()
	slices.Sort(env)
	return env
}

type Entry struct {
	Path   string
	Mode   fs.FileMode
	Size   int64
	Digest [sha256.Size]byte
	Link   string
}

func Tree(path string) (entries []Entry, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("snapshot root must be a directory, not a link")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	err = fs.WalkDir(root.FS(), ".", func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entry, err := snapshotEntry(root, path)
		if err == nil {
			entries = append(entries, entry)
		}
		return err
	})
	return entries, err
}

func snapshotEntry(root *os.Root, path string) (Entry, error) {
	info, err := root.Lstat(path)
	if err != nil {
		return Entry{}, err
	}
	entry := Entry{Path: path, Mode: info.Mode()}
	switch {
	case info.Mode().IsRegular():
		entry.Size = info.Size()
		file, err := root.Open(path)
		if err != nil {
			return Entry{}, err
		}
		digest := sha256.New()
		_, readErr := io.Copy(digest, file)
		if err := errors.Join(readErr, file.Close()); err != nil {
			return Entry{}, err
		}
		copy(entry.Digest[:], digest.Sum(nil))
	case info.Mode()&os.ModeSymlink != 0:
		entry.Size = info.Size()
		entry.Link, err = root.Readlink(path)
	case info.IsDir():
	default:
		err = errors.New("snapshot refuses special files")
	}
	return entry, err
}

func Preserve(t testing.TB, root string) {
	t.Helper()
	env := Environment()
	tree, err := Tree(root)
	require.NoError(t, err)
	t.Cleanup(func() {
		if !slices.Equal(env, Environment()) {
			t.Error("process environment changed")
		}
		after, err := Tree(root)
		require.NoError(t, err)
		require.Equal(t, tree, after, "external tree changed")
	})
}

type ForbiddenEffects struct {
	processes atomic.Int64
	network   atomic.Int64
}

func (s *ForbiddenEffects) Run(context.Context, string, ...string) error {
	s.processes.Add(1)
	return errors.New("process execution forbidden")
}

func (s *ForbiddenEffects) DialContext(context.Context, string, string) (net.Conn, error) {
	s.network.Add(1)
	return nil, errors.New("network dial forbidden")
}

func (s *ForbiddenEffects) RoundTrip(*http.Request) (*http.Response, error) {
	s.network.Add(1)
	return nil, errors.New("HTTP request forbidden")
}

// Check retains evidence when callers swallow an effect's error.
func (s *ForbiddenEffects) Check() error {
	if s.processes.Load() != 0 || s.network.Load() != 0 {
		return errors.New("forbidden process or network effect attempted")
	}
	return nil
}

type QualificationRoot struct {
	path string
	root *os.Root
}

func NewQualificationRoot(t testing.TB) *QualificationRoot {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	root, err := os.OpenRoot(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	return &QualificationRoot{path: path, root: root}
}

func (q *QualificationRoot) Path() string { return q.path }

func (q *QualificationRoot) Validate(homes ...string) error {
	if len(homes) == 0 {
		return errors.New("qualification homes required")
	}
	current, err := os.Lstat(q.path)
	if err != nil {
		return err
	}
	allocated, err := q.root.Stat(".")
	if err != nil {
		return err
	}
	if !current.IsDir() || !os.SameFile(current, allocated) {
		return errors.New("qualification root was replaced")
	}
	for _, home := range homes {
		if err := q.validateHome(home); err != nil {
			return err
		}
	}
	return nil
}

func (q *QualificationRoot) validateHome(home string) error {
	// Cleaning first can hide a symlink traversed before a parent component.
	if !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return errors.New("qualification home must be a clean absolute path")
	}
	rel, err := filepath.Rel(q.path, home)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return errors.New("qualification home must be beneath the allocated temporary root")
	}
	return q.validateDirectories(rel)
}

func (q *QualificationRoot) validateDirectories(rel string) error {
	// Reject links before inspecting their targets, including fake user homes.
	path := ""
	for component := range strings.SplitSeq(rel, string(filepath.Separator)) {
		path = filepath.Join(path, component)
		info, err := q.root.Lstat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errors.New("qualification home must contain only directories, without links")
		}
	}
	return nil
}
