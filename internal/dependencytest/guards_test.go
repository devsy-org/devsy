package dependencytest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvironmentDetectsMutation(t *testing.T) {
	t.Setenv("DEVSY_GUARD_SENTINEL", "before")
	before := Environment()
	require.Equal(t, before, Environment())
	t.Setenv("DEVSY_GUARD_SENTINEL", "after")
	require.NotEqual(t, before, Environment())
}

func TestTreeDetectsMutation(t *testing.T) {
	mutations := map[string]func(string) error{
		"content": func(path string) error { return os.WriteFile(path, []byte("other"), 0o600) },
		"size":    func(path string) error { return os.WriteFile(path, []byte("longer"), 0o600) },
		"path":    func(path string) error { return os.Rename(path, path+".renamed") },
		"delete":  os.Remove,
		"create":  func(path string) error { return os.WriteFile(path+".new", nil, 0o600) },
		"mode":    func(path string) error { return os.Chmod(path, 0o400) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "external")
			require.NoError(t, os.WriteFile(path, []byte("bytes"), 0o600))
			before, err := Tree(root)
			require.NoError(t, err)
			require.NoError(t, mutate(path))
			after, err := Tree(root)
			require.NoError(t, err)
			require.NotEqual(t, before, after)
		})
	}
}

func TestPreserveAllowsReads(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "external")
	require.NoError(t, os.WriteFile(path, []byte("bytes"), 0o600))
	Preserve(t, root)
	reader, err := os.OpenRoot(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reader.Close()) })
	data, err := reader.ReadFile("external")
	require.NoError(t, err)
	require.Equal(t, "bytes", string(data))
}

func TestTreeDoesNotFollowLinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(outside, link))
	before, err := Tree(root)
	require.NoError(t, err)
	require.Len(t, before, 2)
	require.Equal(t, outside, before[1].Link)
	require.NoError(t, os.WriteFile(filepath.Join(outside, "unobserved"), []byte("data"), 0o600))
	after, err := Tree(root)
	require.NoError(t, err)
	require.Equal(t, before, after)
	_, err = Tree(link)
	require.Error(t, err)
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(filepath.Join(outside, "missing"), link))
	after, err = Tree(root)
	require.NoError(t, err)
	require.NotEqual(t, before, after)
}

func TestForbiddenEffects(t *testing.T) {
	for name, attempt := range map[string]func(*ForbiddenEffects) error{
		"process": func(s *ForbiddenEffects) error { return s.Run(context.Background(), "never-run") },
		"dial": func(s *ForbiddenEffects) error {
			conn, err := s.DialContext(context.Background(), "tcp", "example.invalid:443")
			require.Nil(t, conn)
			return err
		},
		"http": func(s *ForbiddenEffects) error {
			response, err := s.RoundTrip(nil)
			require.Nil(t, response)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			spy := &ForbiddenEffects{}
			require.NoError(t, spy.Check())
			require.Error(t, attempt(spy))
			require.Error(t, spy.Check())
		})
	}
}

func TestQualificationHomes(t *testing.T) {
	q := NewQualificationRoot(t)
	devsy, msb := filepath.Join(q.Path(), "devsy"), filepath.Join(q.Path(), "msb")
	require.NoError(t, os.Mkdir(devsy, 0o700))
	require.NoError(t, os.Mkdir(msb, 0o700))
	Preserve(t, q.Path())
	require.NoError(t, q.Validate(devsy, msb))
	fakeUserHome := t.TempDir()
	Preserve(t, fakeUserHome)
	for _, path := range []string{fakeUserHome, q.Path(), "relative", "", q.Path() + "-sibling"} {
		require.Error(t, q.Validate(path))
	}
	require.Error(t, q.Validate())
	require.Error(t, q.Validate(filepath.Join(q.Path(), "missing")))
	link := filepath.Join(q.Path(), "escape")
	require.NoError(t, os.Symlink(fakeUserHome, link))
	t.Cleanup(func() { require.NoError(t, os.Remove(link)) })
	require.Error(t, q.Validate(link))
	require.Error(t, q.Validate(filepath.Join(link, ".microsandbox")))
}

func TestQualificationRejectsReplacedRoot(t *testing.T) {
	q := NewQualificationRoot(t)
	fakeUserHome := t.TempDir()
	Preserve(t, fakeUserHome)
	require.NoError(t, os.Remove(q.Path()))
	require.NoError(t, os.Symlink(fakeUserHome, q.Path()))
	require.Error(t, q.Validate(filepath.Join(q.Path(), ".microsandbox")))
}

func TestQualificationRejectsUncleanSymlinkPath(t *testing.T) {
	q := NewQualificationRoot(t)
	fakeOutside := t.TempDir()
	localHome := filepath.Join(q.Path(), "home")
	outsideHome := filepath.Join(fakeOutside, "home")
	nested := filepath.Join(fakeOutside, "nested")
	for _, path := range []string{localHome, outsideHome, nested} {
		require.NoError(t, os.Mkdir(path, 0o700))
	}
	require.NoError(t, os.Symlink(nested, filepath.Join(q.Path(), "escape")))
	Preserve(t, q.Path())
	Preserve(t, fakeOutside)
	sep := string(filepath.Separator)
	raw := q.Path() + sep + "escape" + sep + ".." + sep + "home"
	resolved, err := filepath.EvalSymlinks(raw)
	require.NoError(t, err)
	canonicalOutside, err := filepath.EvalSymlinks(outsideHome)
	require.NoError(t, err)
	require.Equal(t, canonicalOutside, resolved)
	require.Equal(t, localHome, filepath.Clean(raw))
	require.NoError(t, q.Validate(localHome))
	require.ErrorContains(t, q.Validate(raw), "clean absolute path")
}

func TestQualificationCleanPaths(t *testing.T) {
	q := NewQualificationRoot(t)
	home := filepath.Join(q.Path(), "runtime \u03bb", "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	Preserve(t, q.Path())
	require.NoError(t, q.Validate(home))
	sep := string(filepath.Separator)
	for _, raw := range []string{
		home + sep,
		q.Path() + sep + sep + "runtime \u03bb" + sep + "home",
		q.Path() + sep + "." + sep + "runtime \u03bb" + sep + "home",
		home + sep + ".." + sep + "home",
	} {
		require.ErrorContains(t, q.Validate(raw), "clean absolute path")
	}
}
