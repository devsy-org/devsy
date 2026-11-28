package dependencies

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

// These tests call the native inspector on this machine's disks. They run on
// Windows only. The shared walk tests cover the same rules with synthetic handles.
type lookupNativeSuite struct {
	suite.Suite
	root string
}

func TestLookupNativeWindows(t *testing.T) { suite.Run(t, new(lookupNativeSuite)) }

func (s *lookupNativeSuite) SetupTest() {
	root, err := filepath.EvalSymlinks(s.T().TempDir())
	s.Require().NoError(err)
	s.root = root
}

func (s *lookupNativeSuite) TestARealExecutableIsFoundAndKeepsItsIdentity() {
	path := s.write(filepath.Join(s.root, "bin", "docker.exe"))
	environment := s.environment(filepath.Dir(path))

	first, err := environment.Lookup(context.Background(), LookupRequest{Command: testDocker})
	s.Require().NoError(err)
	second, err := environment.Lookup(context.Background(), LookupRequest{Command: testDocker})
	s.Require().NoError(err)

	s.Require().Len(first.Candidates, 1)
	s.Equal(FileExecutable, first.Candidates[0].State)
	s.Equal(path, first.Candidates[0].ResolvedPath)
	s.Empty(first.ReasonCode)
	s.Require().NotNil(first.Candidates[0].Identity)
	s.Require().Len(second.Candidates, 1)
	s.True(first.Candidates[0].Identity.SameFile(second.Candidates[0].Identity))
}

func (s *lookupNativeSuite) TestCaseAliasesOfOneDirectoryGiveOneCandidate() {
	directory := filepath.Dir(s.write(filepath.Join(s.root, "bin", "docker.exe")))
	environment := s.environment(strings.ToUpper(directory) + ";" + directory)

	result, err := environment.Lookup(context.Background(), LookupRequest{Command: testDocker})

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal(FileExecutable, result.Candidates[0].State)
}

func (s *lookupNativeSuite) TestAMissingFileLeavesNoCandidate() {
	directory := filepath.Join(s.root, "bin")
	s.Require().NoError(os.MkdirAll(directory, 0o700))

	result, err := s.environment(directory).Lookup(
		context.Background(), LookupRequest{Command: testDocker})

	s.Require().NoError(err)
	s.Empty(result.Candidates)
	s.Equal(LookupMissing, result.ReasonCode)
}

func (s *lookupNativeSuite) TestFileKindsGetTheirOwnState() {
	plain := s.write(filepath.Join(s.root, "plain", "docker.txt"))
	directory := filepath.Join(s.root, "dir", "docker.exe")
	s.Require().NoError(os.MkdirAll(directory, 0o700))
	for name, want := range map[string]struct {
		path  string
		state FileState
	}{
		"no invocation suffix": {plain, FileNonexecutable},
		"directory":            {directory, FileNonregular},
	} {
		result, err := s.environment("").Lookup(context.Background(), LookupRequest{
			Command: testDocker, ReviewedLocations: []string{want.path},
		})

		s.Require().NoError(err, name)
		s.Require().Len(result.Candidates, 1, name)
		s.Equal(want.state, result.Candidates[0].State, name)
	}
}

func (s *lookupNativeSuite) TestALinkedDirectoryIsDeferredNotFollowed() {
	target := filepath.Dir(s.write(filepath.Join(s.root, "real", "docker.exe")))
	link := filepath.Join(s.root, "link")
	if err := os.Symlink(target, link); err != nil {
		s.T().Skipf("symbolic links are not available here: %v", err)
	}

	result, err := s.environment("").Lookup(context.Background(), LookupRequest{
		Command: testDocker, ReviewedLocations: []string{filepath.Join(link, "docker.exe")},
	})

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal(FileDeferred, result.Candidates[0].State)
	s.True(result.Candidates[0].IsSymlink)
	s.Nil(result.Candidates[0].Identity)
	s.Equal(LookupInspectionDeferred, result.ReasonCode)
}

func (s *lookupNativeSuite) TestAJunctionIsDeferredNotFollowed() {
	target := filepath.Dir(s.write(filepath.Join(s.root, "real", "docker.exe")))
	junction := filepath.Join(s.root, "junction")
	out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, target).CombinedOutput()
	if err != nil {
		s.T().Skipf("junctions are not available here: %v: %s", err, out)
	}

	result, err := s.environment("").Lookup(context.Background(), LookupRequest{
		Command: testDocker, ReviewedLocations: []string{filepath.Join(junction, "docker.exe")},
	})

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal(FileDeferred, result.Candidates[0].State)
	s.True(result.Candidates[0].IsSymlink)
	s.Nil(result.Candidates[0].Identity)
}

func (s *lookupNativeSuite) TestALinkedFinalFileIsDeferredNotFollowed() {
	tool := s.write(filepath.Join(s.root, "real", "tool.exe"))
	link := filepath.Join(s.root, "bin", "docker.exe")
	s.Require().NoError(os.MkdirAll(filepath.Dir(link), 0o700))
	if err := os.Symlink(tool, link); err != nil {
		s.T().Skipf("symbolic links are not available here: %v", err)
	}

	result, err := s.environment("").Lookup(context.Background(), LookupRequest{
		Command: testDocker, ReviewedLocations: []string{link},
	})

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal(FileDeferred, result.Candidates[0].State)
	s.True(result.Candidates[0].IsSymlink)
	s.Nil(result.Candidates[0].Identity)
}

func (s *lookupNativeSuite) TestAFileInTheMiddleOfThePathIsMissing() {
	file := s.write(filepath.Join(s.root, "plain", "file"))

	result, err := s.environment("").Lookup(context.Background(), LookupRequest{
		Command: testDocker, ReviewedLocations: []string{filepath.Join(file, "docker.exe")},
	})

	s.Require().NoError(err)
	s.Empty(result.Candidates)
	s.Equal(LookupMissing, result.ReasonCode)
}

func (s *lookupNativeSuite) TestATrailingSeparatorNeverYieldsAnExecutable() {
	path := s.write(filepath.Join(s.root, "bin", "docker.exe"))

	result, err := s.environment("").Lookup(context.Background(), LookupRequest{
		Command: testDocker, ReviewedLocations: []string{path + `\`},
	})

	s.Require().NoError(err)
	for _, candidate := range result.Candidates {
		s.NotEqual(FileExecutable, candidate.State)
	}
}

func (s *lookupNativeSuite) TestTheSystemCommandInterpreterIsFoundOnTheDiskVolume() {
	root := os.Getenv("SystemRoot")
	if root == "" {
		s.T().Skip("SystemRoot is not set")
	}
	cmd := filepath.Join(root, "System32", "cmd.exe")

	result, err := s.environment("").Lookup(context.Background(), LookupRequest{
		Command: "cmd", ReviewedLocations: []string{cmd},
	})

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal(FileExecutable, result.Candidates[0].State)
	s.NotNil(result.Candidates[0].Identity)
}

func (s *lookupNativeSuite) TestAVerbatimLocationIsDeferredWithoutOpeningIt() {
	root := os.Getenv("SystemRoot")
	if root == "" {
		s.T().Skip("SystemRoot is not set")
	}

	result, err := s.environment("").Lookup(context.Background(), LookupRequest{
		Command:           "cmd",
		ReviewedLocations: []string{`\\?\` + filepath.Join(root, "System32", "cmd.exe")},
	})

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal(FileDeferred, result.Candidates[0].State)
	s.Nil(result.Candidates[0].Identity)
}

func (s *lookupNativeSuite) TestACanceledContextStopsBeforeAnyMetadata() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.environment(s.root).Lookup(ctx, LookupRequest{Command: testDocker})

	s.Require().ErrorIs(err, context.Canceled)
}

func (s *lookupNativeSuite) write(path string) string {
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o700))
	s.Require().NoError(os.WriteFile(path, []byte("data"), 0o600))
	return path
}

func (s *lookupNativeSuite) environment(path string) LookupEnvironment {
	environment, err := NewLookupEnvironment(map[string]string{envPath: path}, s.root)
	s.Require().NoError(err)
	return environment
}
