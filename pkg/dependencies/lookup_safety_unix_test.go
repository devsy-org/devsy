//go:build !windows

package dependencies

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type lookupUnixSuite struct {
	suite.Suite
	root string
}

func TestLookupUnix(t *testing.T) { suite.Run(t, new(lookupUnixSuite)) }

// The physical root keeps a link such as /var on macOS out of every expectation.
func (s *lookupUnixSuite) SetupTest() {
	root, err := filepath.EvalSymlinks(s.T().TempDir())
	s.Require().NoError(err)
	s.root = root
}

func (s *lookupUnixSuite) TestResolutionMatchesTheKernelForLinksAndDotDot() {
	s.Require().NoError(os.MkdirAll(filepath.Join(s.root, "other", "child"), 0o700))
	s.write(filepath.Join(s.root, "other", testDocker), 0o755)
	for name, target := range map[string]string{
		testRelative: "other/child", "absolute": filepath.Join(s.root, "other", "child"),
		"chain": testRelative, "broken": "absent", "loop": "loop",
	} {
		s.Require().NoError(os.Symlink(target, filepath.Join(s.root, name)))
	}
	for _, suffix := range []string{
		"/relative/../docker", "/absolute/../docker", "/chain/../docker",
		"/other/./child/..//docker", "/other/docker/", "/other/docker/..",
		"/broken", "/loop", "/relative/../../..", "/absolute/.",
	} {
		want, wantErr := filepath.EvalSymlinks(s.root + suffix)
		got, err := resolveLookupPath(context.Background(), s.root+suffix)
		if wantErr != nil {
			s.Require().Error(err, suffix)
			continue
		}
		s.Require().NoError(err, suffix)
		s.Equal(want, got, suffix)
	}
}

func (s *lookupUnixSuite) TestExecutableFileIsIdentifiedFromTheOpenedFile() {
	path := s.write(filepath.Join(s.root, "bin", testDocker), 0o755)

	candidate := s.observe(path)

	s.Equal(FileExecutable, candidate.State)
	s.Empty(candidate.ReasonCode)
	s.Equal(path, candidate.ResolvedPath)
	s.False(candidate.IsSymlink)
	s.Require().NotNil(candidate.Identity)
	info, err := os.Stat(path)
	s.Require().NoError(err)
	s.True(candidate.Identity.SameFile(&FileObservation{info: info}))
}

func (s *lookupUnixSuite) TestFileKindsGetTheirOwnState() {
	s.write(filepath.Join(s.root, "plain", testDocker), 0o644)
	s.Require().NoError(os.MkdirAll(filepath.Join(s.root, "dir", testDocker), 0o700))
	s.Require().NoError(os.MkdirAll(filepath.Join(s.root, "link"), 0o700))
	s.Require().NoError(os.Symlink("absent", filepath.Join(s.root, "link", testDocker)))
	for name, want := range map[string]struct {
		path  string
		state FileState
	}{
		"nonexecutable": {filepath.Join(s.root, "plain", testDocker), FileNonexecutable},
		"directory":     {filepath.Join(s.root, "dir", testDocker), FileNonregular},
		"broken link":   {filepath.Join(s.root, "link", testDocker), FileBrokenLink},
	} {
		candidate := s.observe(want.path)

		s.Equal(want.state, candidate.State, name)
		s.Equal(want.state == FileNonexecutable, candidate.Identity != nil, name)
	}
}

func (s *lookupUnixSuite) TestLinksReportTheirTarget() {
	target := s.write(filepath.Join(s.root, "real", testDocker), 0o755)
	s.Require().NoError(os.MkdirAll(filepath.Join(s.root, "alias"), 0o700))
	s.Require().NoError(os.Symlink(target, filepath.Join(s.root, "alias", testDocker)))
	s.Require().NoError(os.Symlink("real", filepath.Join(s.root, "dirlink")))
	for _, path := range []string{
		filepath.Join(s.root, "alias", testDocker), filepath.Join(s.root, "dirlink", testDocker),
	} {
		candidate := s.observe(path)

		s.Equal(FileExecutable, candidate.State, path)
		s.True(candidate.IsSymlink, path)
		s.Equal(target, candidate.ResolvedPath, path)
		s.NotNil(candidate.Identity, path)
	}
}

func (s *lookupUnixSuite) TestOnlyRegularFilesAreOpened() {
	pipe := filepath.Join(s.root, "pipe")
	s.Require().NoError(syscall.Mkfifo(pipe, 0o755))
	directory := filepath.Join(s.root, "directory")
	s.Require().NoError(os.Mkdir(directory, 0o700))
	regular := s.write(filepath.Join(s.root, "regular"), 0o755)
	var opened []string
	count := func(path string) (lookupHandle, error) {
		opened = append(opened, path)
		return openLookupFile(path)
	}

	for _, path := range []string{pipe, directory, regular} {
		_, err := openedFileInfo(context.Background(), path, count)
		s.Require().NoError(err, path)
	}

	s.Equal([]string{regular}, opened)
	s.Equal(FileNonregular, s.observe(pipe).State)
	s.Nil(s.observe(pipe).Identity)
}

func (s *lookupUnixSuite) TestLinkChainsAreCappedAtTheKernelLimit() {
	target := s.write(filepath.Join(s.root, "target"), 0o755)
	previous := target
	for i := range 41 {
		link := filepath.Join(s.root, "link"+strconv.Itoa(i))
		s.Require().NoError(os.Symlink(previous, link))
		previous = link
		if i == 39 {
			resolved, err := resolveLookupPath(context.Background(), link)
			s.Require().NoError(err)
			s.Equal(target, resolved)
		}
	}

	_, err := resolveLookupPath(context.Background(), previous)

	s.Require().ErrorIs(err, syscall.ELOOP)
}

func (s *lookupUnixSuite) TestAnEmptyLinkTargetIsMissing() {
	walk := unixLookupPath{resolved: "/"}

	err := walk.followLink(s.root+"/link", "", false)

	s.Require().ErrorIs(err, syscall.ENOENT)
}

func (s *lookupUnixSuite) TestDeniedDirectoryIsInaccessibleNotMissing() {
	s.skipAsRoot()
	locked := filepath.Join(s.root, "locked")
	s.Require().NoError(os.Mkdir(locked, 0o000))
	s.T().Cleanup(func() { _ = os.Remove(locked) })
	path := filepath.Join(locked, testDocker)

	candidate := s.observe(path)

	s.Equal(FileInaccessible, candidate.State)
	s.Equal(LookupInaccessible, candidate.ReasonCode)
}

func (s *lookupUnixSuite) TestUnreadableExecutableIsInaccessible() {
	s.skipAsRoot()
	path := s.write(filepath.Join(s.root, "bin", testDocker), 0o111)

	candidate := s.observe(path)

	s.Equal(FileInaccessible, candidate.State)
	s.Equal(LookupInaccessible, candidate.ReasonCode)
	s.Nil(candidate.Identity)
}

func (s *lookupUnixSuite) TestOpenedFileRefusesAPathSwap() {
	named := s.write(filepath.Join(s.root, "named"), 0o755)
	other := s.write(filepath.Join(s.root, "other"), 0o755)
	namedInfo, err := os.Lstat(named)
	s.Require().NoError(err)

	info, err := statOpenedFile(context.Background(), named, namedInfo, openLookupFile)
	s.Require().NoError(err)
	s.True(os.SameFile(namedInfo, info))

	_, err = statOpenedFile(context.Background(), other, namedInfo, openLookupFile)
	s.Require().ErrorIs(err, errLookupFileChanged)
}

func (s *lookupUnixSuite) TestOpenedFileNeverFollowsAFinalLink() {
	target := s.write(filepath.Join(s.root, "target"), 0o755)
	link := filepath.Join(s.root, "link")
	s.Require().NoError(os.Symlink(target, link))
	targetInfo, err := os.Lstat(target)
	s.Require().NoError(err)

	_, err = statOpenedFile(context.Background(), link, targetInfo, openLookupFile)

	s.Require().ErrorIs(err, syscall.ELOOP)
}

func (s *lookupUnixSuite) TestInspectionRefusesPathsThatAreNotLocalAbsolute() {
	for _, path := range []string{
		testDocker, "./" + testDocker, `C:\Tools\docker.exe`, `\\host\share\docker`, s.root + "/a\x00b",
	} {
		_, err := inspectLookupFile(context.Background(), path)

		s.Require().ErrorIs(err, syscall.EINVAL, path)
	}
}

func (s *lookupUnixSuite) TestInspectionStopsWhenTheContextIsDone() {
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancel()

	observed, err := inspectLookupFile(ctx, filepath.Join(s.root, "missing"))

	s.Require().ErrorIs(err, context.DeadlineExceeded)
	s.Equal(lookupFile{}, observed)
}

func (s *lookupUnixSuite) TestLookupReportsRealFilesInPathOrder() {
	empty := filepath.Join(s.root, "empty")
	s.Require().NoError(os.MkdirAll(empty, 0o700))
	first := s.write(filepath.Join(s.root, "one", testDocker), 0o755)
	reviewed := s.write(filepath.Join(s.root, "reviewed", testDocker), 0o755)
	list := empty + ":" + filepath.Dir(first) + ":" + filepath.Dir(first)

	result, err := s.environment(list).Lookup(context.Background(), LookupRequest{
		Command: testDocker, ReviewedLocations: []string{reviewed},
	})

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 2)
	s.Equal(first, result.Candidates[0].LexicalPath)
	s.Equal(LookupPATH, result.Candidates[0].Origin)
	s.Equal(reviewed, result.Candidates[1].LexicalPath)
	s.Equal(LookupReviewed, result.Candidates[1].Origin)
	s.Equal(FileExecutable, result.Candidates[1].State)
	s.Empty(result.ReasonCode)
	s.False(result.Executed)
}

func (s *lookupUnixSuite) TestOpenFlagsNeverBlockFollowOrAttachATerminal() {
	for name, flag := range map[string]int{
		"nonblock": syscall.O_NONBLOCK, "nofollow": syscall.O_NOFOLLOW, "noctty": syscall.O_NOCTTY,
	} {
		s.NotZero(lookupOpenFlags&flag, name)
	}
}

// Without O_NONBLOCK, opening a pipe for reading waits for a writer forever.
func (s *lookupUnixSuite) TestAPipeSwappedInBeforeTheOpenNeverBlocks() {
	path := s.write(filepath.Join(s.root, testDocker), 0o755)
	named, err := os.Lstat(path)
	s.Require().NoError(err)
	swapInAPipe := func(path string) (lookupHandle, error) {
		if err := os.Remove(path); err != nil {
			return nil, err
		}
		if err := syscall.Mkfifo(path, 0o755); err != nil {
			return nil, err
		}
		return openLookupFile(path)
	}
	done := make(chan error, 1)

	go func() {
		_, err := statOpenedFile(context.Background(), path, named, swapInAPipe)
		done <- err
	}()

	select {
	case err := <-done:
		s.Require().ErrorIs(err, errLookupFileChanged)
	case <-time.After(3 * time.Second):
		writer, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			_ = writer.Close()
		}
		s.FailNow("opening a pipe swapped in before the open blocked")
	}
}

// A file system may hand the inode number of a removed file to the pipe that
// replaces it, so the identity comparison alone cannot catch the swap.
func (s *lookupUnixSuite) TestAnOpenedFileThatIsNotRegularIsRefusedEvenWithTheSameIdentity() {
	path := filepath.Join(s.root, "pipe")
	s.Require().NoError(syscall.Mkfifo(path, 0o755))
	named, err := os.Lstat(path)
	s.Require().NoError(err)

	_, err = statOpenedFile(context.Background(), path, named, openLookupFile)

	s.Require().ErrorIs(err, errLookupFileChanged)
}

func (s *lookupUnixSuite) TestTheDescriptorIsClosedExactlyOnceOnEveryPath() {
	named := s.write(filepath.Join(s.root, "named"), 0o755)
	other := s.write(filepath.Join(s.root, "other"), 0o755)
	namedInfo, err := os.Lstat(named)
	s.Require().NoError(err)
	errFstat := errors.New("fstat failed")
	cancelAfter := func(_ *trackedHandle, cancel context.CancelFunc) { cancel() }
	failFstat := func(handle *trackedHandle, _ context.CancelFunc) { handle.statErr = errFstat }
	for name, c := range map[string]struct {
		path  string
		after func(*trackedHandle, context.CancelFunc)
		want  error
	}{
		"success":        {path: named},
		"another file":   {path: other, want: errLookupFileChanged},
		"canceled after": {path: named, after: cancelAfter, want: context.Canceled},
		"fstat failure":  {path: named, after: failFstat, want: errFstat},
	} {
		var handles []*trackedHandle
		ctx, cancel := context.WithCancel(context.Background())

		_, err := statOpenedFile(ctx, c.path, namedInfo, trackingOpener(&handles, cancel, c.after))
		cancel()

		if c.want == nil {
			s.Require().NoError(err, name)
		} else {
			s.Require().ErrorIs(err, c.want, name)
		}
		s.Require().Len(handles, 1, name)
		s.Equal(1, handles[0].closes, name)
	}
}

func (s *lookupUnixSuite) write(path string, mode os.FileMode) string {
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o700))
	s.Require().NoError(os.WriteFile(path, []byte("data"), mode))
	s.Require().NoError(os.Chmod(path, mode))
	return path
}

func (s *lookupUnixSuite) environment(path string) LookupEnvironment {
	environment, err := NewLookupEnvironment(map[string]string{envPath: path}, s.root)
	s.Require().NoError(err)
	return environment
}

func (s *lookupUnixSuite) observe(path string) ExecutableCandidate {
	candidate, err := s.environment("").observe(context.Background(), path, LookupPATH)
	s.Require().NoError(err)
	return candidate
}

func (s *lookupUnixSuite) skipAsRoot() {
	if os.Geteuid() == 0 {
		s.T().Skip("permission checks do not apply to root")
	}
}

// trackedHandle counts closes and can fail the descriptor stat on demand.
type trackedHandle struct {
	lookupHandle
	statErr error
	closes  int
}

func (h *trackedHandle) Stat() (os.FileInfo, error) {
	if h.statErr != nil {
		return nil, h.statErr
	}
	return h.lookupHandle.Stat()
}

func (h *trackedHandle) Close() error {
	h.closes++
	return h.lookupHandle.Close()
}

func trackingOpener(
	handles *[]*trackedHandle,
	cancel context.CancelFunc,
	after func(*trackedHandle, context.CancelFunc),
) lookupOpener {
	return func(path string) (lookupHandle, error) {
		file, err := openLookupFile(path)
		if err != nil {
			return nil, err
		}
		handle := &trackedHandle{lookupHandle: file}
		*handles = append(*handles, handle)
		if after != nil {
			after(handle, cancel)
		}
		return handle, nil
	}
}
