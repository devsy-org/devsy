package dependencytest

import (
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"os"
	"slices"
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
