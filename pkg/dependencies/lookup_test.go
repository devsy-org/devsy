package dependencies_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/devsy-org/devsy/pkg/dependencies"
	"github.com/stretchr/testify/suite"
)

const (
	lookupTestExtension = ".exe"
	lookupTestPATH      = "PATH"
)

type lookupSuite struct {
	suite.Suite
	root string
}

func TestLookup(t *testing.T) { suite.Run(t, new(lookupSuite)) }

func (s *lookupSuite) SetupTest() {
	s.root = s.T().TempDir()
}

func (s *lookupSuite) TestSnapshotPATHAndPassiveGuards() {
	selected := s.executable("supplied/docker")
	s.executable("ambient/docker")
	s.executable(providerDocker)
	s.executable("relative/docker")
	separator := string(filepath.ListSeparator)
	values := map[string]string{
		lookupTestPATH: separator + "." + separator + "relative" + separator + s.root + separator + filepath.Dir(
			selected,
		) + separator,
		"DOCKER_HOST":       "",
		"DOCKER_TLS_VERIFY": "",
		"UNRELATED":         "original",
	}
	s.T().Setenv(lookupTestPATH, filepath.Join(s.root, "ambient"))
	environment := s.environment(values)
	values[lookupTestPATH] = filepath.Join(s.root, "ambient")
	delete(values, "DOCKER_HOST")
	values["UNRELATED"] = "changed"
	cwd, err := os.Getwd()
	s.Require().NoError(err)
	dependencytest.Preserve(s.T(), s.root)
	result := s.lookup(environment, dependencies.LookupRequest{Command: providerDocker})
	s.Require().Len(result.Candidates, 1)
	s.Equal(selected, result.Candidates[0].LexicalPath)
	s.Equal(dependencies.LookupPATH, result.Candidates[0].Origin)
	s.Equal(dependencies.FileExecutable, result.Candidates[0].State)
	s.Empty(result.ReasonCode)
	value, present := environment.Value("DOCKER_HOST")
	s.True(present)
	s.Empty(value)
	value, present = environment.Value("UNRELATED")
	s.True(present)
	s.Equal("original", value)
	_, present = environment.Value("ABSENT")
	s.False(present)
	after, err := os.Getwd()
	s.Require().NoError(err)
	s.Equal(cwd, after)
	s.NoFileExists(filepath.Join(s.root, "EXECUTED"))
}

func (s *lookupSuite) TestAbsentAndEmptyPATHNeverUseAmbientOrCWD() {
	s.executable(providerDocker)
	ambient := s.executable("ambient/docker")
	s.T().Setenv(lookupTestPATH, filepath.Dir(ambient))
	dependencytest.Preserve(s.T(), s.root)
	for _, values := range []map[string]string{nil, {lookupTestPATH: ""}, {lookupTestPATH: "."}} {
		result := s.lookup(
			s.environment(values),
			dependencies.LookupRequest{Command: providerDocker},
		)
		s.Empty(result.Candidates)
		s.Equal(dependencies.LookupMissing, result.ReasonCode)
	}
}

func (s *lookupSuite) TestExplicitSameAsDefaultRetainsIntent() {
	first := s.executable("first/docker")
	s.executable("second/docker")
	reviewed := s.executable("reviewed/docker")
	environment := s.environment(map[string]string{
		lookupTestPATH: filepath.Dir(
			first,
		) + string(
			filepath.ListSeparator,
		) + filepath.Join(
			s.root,
			"second",
		),
	})
	dependencytest.Preserve(s.T(), s.root)
	result := s.lookup(environment, dependencies.LookupRequest{
		Command: providerDocker, Explicit: true, ReviewedLocations: []string{reviewed},
	})
	s.Require().Len(result.Candidates, 1)
	s.Equal(first, result.Candidates[0].LexicalPath)
	s.Equal(dependencies.LookupExplicit, result.Candidates[0].Origin)
	environmentChoice := s.lookup(environment, dependencies.LookupRequest{
		Command: providerDocker, Explicit: true, ExplicitOrigin: dependencies.LookupExplicitEnv,
	})
	s.Require().Len(environmentChoice.Candidates, 1)
	s.Equal(dependencies.LookupExplicitEnv, environmentChoice.Candidates[0].Origin)
}

func (s *lookupSuite) TestInvalidExplicitNeverFallsBack() {
	fallback := s.executable("fallback/docker")
	environment := s.environment(map[string]string{lookupTestPATH: filepath.Dir(fallback)})
	dependencytest.Preserve(s.T(), s.root)
	for _, command := range []string{"", "missing", filepath.Join(s.root, "missing"), "bad\x00path"} {
		result := s.lookup(environment, dependencies.LookupRequest{
			Command: command, Explicit: true, ReviewedLocations: []string{fallback},
		})
		s.Equal(dependencies.LookupInvalidPath, result.ReasonCode)
		for _, candidate := range result.Candidates {
			s.NotEqual(fallback, candidate.LexicalPath)
		}
	}
}

func (s *lookupSuite) TestOrderedPATHThenReviewedAndDeduplication() {
	first := s.executable("first/docker")
	second := s.executable("second/docker")
	reviewed := s.executable("reviewed/docker")
	locations := []string{reviewed, first}
	environment := s.environment(map[string]string{
		lookupTestPATH: strings.Join(
			[]string{filepath.Dir(first), filepath.Dir(second), filepath.Dir(first)},
			string(filepath.ListSeparator),
		),
	})
	dependencytest.Preserve(s.T(), s.root)
	request := dependencies.LookupRequest{Command: providerDocker, ReviewedLocations: locations}
	result := s.lookup(environment, request)
	s.Require().Len(result.Candidates, 3)
	for index, expected := range []string{first, second, reviewed} {
		s.Equal(expected, result.Candidates[index].LexicalPath)
	}
	s.Equal(dependencies.LookupReviewed, result.Candidates[2].Origin)
	s.Equal(result, s.lookup(environment, request))
	locations[0] = filepath.Join(s.root, "missing")
	s.Equal(reviewed, result.Candidates[2].LexicalPath)
}

func (s *lookupSuite) TestExplicitRelativeAndExactSpacesUnicode() {
	path := s.executable(" spaced \u96ea dir/docker tool ")
	name, err := filepath.Rel(s.root, path)
	s.Require().NoError(err)
	dependencytest.Preserve(s.T(), s.root)
	result := s.lookup(
		s.environment(nil),
		dependencies.LookupRequest{Command: name, Explicit: true},
	)
	s.Require().Len(result.Candidates, 1)
	s.Equal(path, result.Candidates[0].LexicalPath)
	s.Equal(dependencies.FileExecutable, result.Candidates[0].State)
	s.Equal(s.root, s.environment(nil).WorkingDirectory())
}

func (s *lookupSuite) TestSymlinkRetargetAndFileIdentity() {
	first := s.executable("versions/first")
	second := s.executable("versions/second")
	link := filepath.Join(s.root, "package-manager")
	if runtime.GOOS == osWindows {
		link += lookupTestExtension
	}
	s.symlink(first, link)
	environment := s.environment(nil)
	request := dependencies.LookupRequest{Command: link, Explicit: true}
	before := s.lookup(environment, request).Candidates[0]
	s.True(before.IsSymlink)
	s.Equal(link, before.LexicalPath)
	if runtime.GOOS == osWindows {
		s.assertDeferred(before)
	} else {
		s.Equal(s.resolved(first), before.ResolvedPath)
	}
	direct := s.lookup(environment, dependencies.LookupRequest{Command: first, Explicit: true}).Candidates[0]
	if runtime.GOOS == osWindows {
		s.Equal(dependencies.FileExecutable, direct.State)
		s.NotNil(direct.Identity)
	} else {
		s.True(before.Identity.SameFile(direct.Identity))
	}
	s.Require().NoError(os.Remove(link))
	s.symlink(second, link)
	after := s.lookup(environment, request).Candidates[0]
	s.Equal(link, after.LexicalPath)
	if runtime.GOOS == osWindows {
		s.assertDeferred(after)
	} else {
		s.Equal(s.resolved(second), after.ResolvedPath)
		s.False(before.Identity.SameFile(after.Identity))
	}
	encoded, err := json.Marshal(after)
	s.Require().NoError(err)
	s.NotContains(string(encoded), "fileIdentity")
	s.Require().NoError(os.Remove(link))
	s.symlink(filepath.Join(s.root, "absent"), link)
	dependencytest.Preserve(s.T(), s.root)
	broken := s.lookup(environment, request)
	if runtime.GOOS == osWindows {
		s.Equal(dependencies.LookupInspectionDeferred, broken.ReasonCode)
		s.assertDeferred(broken.Candidates[0])
	} else {
		s.Equal(dependencies.LookupInvalidPath, broken.ReasonCode)
		s.Equal(dependencies.FileBrokenLink, broken.Candidates[0].State)
	}
	s.Nil(broken.Candidates[0].Identity)
}

func (s *lookupSuite) TestDirectoryNonexecutableAndMissing() {
	file := s.executable("not-executable")
	s.Require().NoError(os.Chmod(file, 0o600))
	dependencytest.Preserve(s.T(), s.root)
	environment := s.environment(nil)
	directory := s.lookup(environment, dependencies.LookupRequest{Command: s.root, Explicit: true})
	s.Equal(dependencies.FileNonregular, directory.Candidates[0].State)
	s.Equal(dependencies.LookupInvalidPath, directory.ReasonCode)
	if runtime.GOOS != osWindows {
		result := s.lookup(environment, dependencies.LookupRequest{Command: file, Explicit: true})
		s.Equal(dependencies.FileNonexecutable, result.Candidates[0].State)
		s.Equal(dependencies.LookupInvalidPath, result.ReasonCode)
	}
	missing := s.lookup(
		environment,
		dependencies.LookupRequest{Command: filepath.Join(s.root, "absent"), Explicit: true},
	)
	s.Equal(dependencies.LookupInvalidPath, missing.ReasonCode)
}

func (s *lookupSuite) TestKnownLaunchersHaveNoComparableTargetIdentity() {
	for _, relative := range []string{
		"home/.local/share/mise/shims/msb", "home/.asdf/shims/msb", "home/.local/share/aquaproj-aqua/bin/msb",
	} {
		path := s.executable(relative)
		environment := s.environment(map[string]string{lookupTestPATH: filepath.Dir(path)})
		result := s.lookup(environment, dependencies.LookupRequest{Command: "msb"})
		s.Require().Len(result.Candidates, 1)
		s.True(result.Candidates[0].Launcher)
		s.Nil(result.Candidates[0].Identity)
		s.Equal(dependencies.LookupIndirectLauncher, result.ReasonCode)
		s.Equal(dependencies.LookupPATH, result.Candidates[0].Origin)
	}
}

func (s *lookupSuite) TestConfiguredLauncherRootAndExplicitStopsAtLauncher() {
	shim := s.executable("custom/shims/msb")
	fallback := s.executable("fallback/msb")
	environment := s.environment(map[string]string{
		"MISE_DATA_DIR": filepath.Join(s.root, "custom"),
		lookupTestPATH: filepath.Dir(
			shim,
		) + string(
			filepath.ListSeparator,
		) + filepath.Dir(
			fallback,
		),
	})
	dependencytest.Preserve(s.T(), s.root)
	result := s.lookup(environment, dependencies.LookupRequest{Command: "msb", Explicit: true})
	s.Require().Len(result.Candidates, 1)
	s.Equal(shim, result.Candidates[0].LexicalPath)
	s.Equal(dependencies.LookupIndirectLauncher, result.ReasonCode)
}

func (s *lookupSuite) TestLiteralShellCharactersAndSymlinkParentTraversal() {
	if runtime.GOOS == osWindows {
		s.T().Skip("Unix path traversal and literal shell filename semantics")
	}
	path := s.executable("literal/$HOME-~-${VALUE}-$(touch EXECUTED)")
	link := filepath.Join(s.root, "link")
	s.Require().NoError(os.MkdirAll(filepath.Join(s.root, "actual", "child"), 0o700))
	s.symlink(filepath.Join(s.root, "actual", "child"), link)
	target := s.executable("actual/docker")
	s.executable(providerDocker)
	dependencytest.Preserve(s.T(), s.root)
	environment := s.environment(nil)
	literal := s.lookup(environment, dependencies.LookupRequest{Command: path, Explicit: true})
	s.Equal(path, literal.Candidates[0].LexicalPath)
	lexical := link + "/../docker"
	traversal := s.lookup(environment, dependencies.LookupRequest{Command: lexical, Explicit: true})
	s.Equal(lexical, traversal.Candidates[0].LexicalPath)
	s.Equal(s.resolved(target), traversal.Candidates[0].ResolvedPath)
}

func (s *lookupSuite) TestPATHSymlinkDotDotPreservesTraversal() {
	if runtime.GOOS == osWindows {
		s.T().Skip("Unix symlink/.. traversal semantics")
	}
	s.Require().NoError(os.MkdirAll(filepath.Join(s.root, "other", "child"), 0o700))
	link := filepath.Join(s.root, "link")
	s.symlink(filepath.Join(s.root, "other", "child"), link)
	target := s.executable("other/docker")
	fallback := s.executable("fallback/docker")
	s.executable(providerDocker)
	directory := link + "/.."
	dependencytest.Preserve(s.T(), s.root)
	result := s.lookup(s.environment(map[string]string{
		lookupTestPATH: directory + string(filepath.ListSeparator) + filepath.Dir(fallback),
	}), dependencies.LookupRequest{Command: providerDocker, Explicit: true})
	s.Require().Len(result.Candidates, 1)
	s.Equal(directory+"/docker", result.Candidates[0].LexicalPath)
	s.Equal(s.resolved(target), result.Candidates[0].ResolvedPath)
}

func (s *lookupSuite) TestPATHRejectsActualWorkingDirectoryAlias() {
	if runtime.GOOS == osWindows {
		s.T().Skip("Unix directory alias fixture; Windows reparse traversal is deferred")
	}
	alias := filepath.Join(s.T().TempDir(), "cwd-alias")
	s.symlink(s.root, alias)
	s.executable(providerDocker)
	fallback := s.executable("fallback/docker")
	dependencytest.Preserve(s.T(), s.root)
	result := s.lookup(s.environment(map[string]string{
		lookupTestPATH: alias + string(filepath.ListSeparator) + filepath.Dir(fallback),
	}), dependencies.LookupRequest{Command: providerDocker, Explicit: true})
	s.Require().Len(result.Candidates, 1)
	s.Equal(fallback, result.Candidates[0].LexicalPath)
}

func (s *lookupSuite) TestScriptSuffixIncludesResolvedTarget() {
	for _, extension := range []string{".cmd", ".BAT", ".ps1"} {
		target := filepath.Join(s.root, "wrapper"+extension)
		s.Require().NoError(os.WriteFile(target, []byte("fixture"), 0o600))
		// #nosec G302 -- Executable mode is required for this passive lookup fixture.
		s.Require().NoError(os.Chmod(target, 0o700))
		link := filepath.Join(s.root, "docker"+extension+".exe")
		s.symlink(target, link)
		result := s.lookup(
			s.environment(nil),
			dependencies.LookupRequest{Command: link, Explicit: true},
		)
		s.Require().Len(result.Candidates, 1)
		if runtime.GOOS == osWindows {
			s.assertDeferred(result.Candidates[0])
		} else {
			s.True(result.Candidates[0].Script, extension)
			s.Equal(dependencies.FileExecutable, result.Candidates[0].State)
		}
	}
	dependencytest.Preserve(s.T(), s.root)
}

func (s *lookupSuite) TestInaccessibleDirectory() {
	if runtime.GOOS == osWindows || os.Getuid() == 0 {
		s.T().Skip("requires native Unix permission enforcement for a non-root user")
	}
	path := s.executable("restricted/docker")
	directory := filepath.Dir(path)
	s.Require().NoError(os.Chmod(directory, 0))
	s.T().Cleanup(func() {
		// #nosec G302 -- Restore traversal permissions on the private test directory.
		s.Require().NoError(os.Chmod(directory, 0o700))
	})
	environment := s.environment(nil)
	result := s.lookup(environment, dependencies.LookupRequest{Command: path, Explicit: true})
	s.Equal(dependencies.FileInaccessible, result.Candidates[0].State)
	s.Equal(dependencies.LookupInvalidPath, result.ReasonCode)
}

func (s *lookupSuite) TestAliasToLauncherDoesNotExposeTargetIdentity() {
	shim := s.executable("home/.asdf/shims/msb")
	alias := filepath.Join(s.root, "alias")
	if runtime.GOOS == osWindows {
		alias += lookupTestExtension
	}
	s.symlink(shim, alias)
	dependencytest.Preserve(s.T(), s.root)
	result := s.lookup(
		s.environment(nil),
		dependencies.LookupRequest{Command: alias, Explicit: true},
	)
	s.Require().Len(result.Candidates, 1)
	if runtime.GOOS == osWindows {
		s.assertDeferred(result.Candidates[0])
		s.Equal(dependencies.LookupInspectionDeferred, result.ReasonCode)
		return
	}
	s.True(result.Candidates[0].Launcher)
	s.Nil(result.Candidates[0].Identity)
	s.Equal(dependencies.LookupIndirectLauncher, result.ReasonCode)
}

func (s *lookupSuite) TestCancellationAndInvalidRequests() {
	environment := s.environment(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := environment.LookupExecutable(ctx, dependencies.LookupRequest{Command: providerDocker})
	s.ErrorIs(err, context.Canceled)
	_, err = dependencies.NewLookupEnvironment(nil, ".")
	s.Error(err)
	_, err = (dependencies.LookupEnvironment{}).LookupExecutable(
		context.Background(),
		dependencies.LookupRequest{Command: providerDocker},
	)
	s.Error(err)
	_, err = environment.LookupExecutable(
		context.Background(),
		dependencies.LookupRequest{Command: "./docker"},
	)
	s.Error(err)
	_, err = environment.LookupExecutable(
		context.Background(),
		dependencies.LookupRequest{
			Command:           providerDocker,
			ReviewedLocations: []string{"relative"},
		},
	)
	s.Error(err)
}

func (s *lookupSuite) environment(values map[string]string) dependencies.LookupEnvironment {
	environment, err := dependencies.NewLookupEnvironment(values, s.root)
	s.Require().NoError(err)
	return environment
}

func (s *lookupSuite) executable(relative string) string {
	path := filepath.Join(s.root, relative)
	if runtime.GOOS == osWindows {
		path += lookupTestExtension
	}
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o700))
	// Any accidental shell invocation leaves evidence inside the guarded tree.
	marker := filepath.Join(s.root, "EXECUTED")
	body := "#!/bin/sh\nprintf executed > '" + strings.ReplaceAll(marker, "'", "'\\''") + "'\n"
	s.Require().NoError(os.WriteFile(path, []byte(body), 0o600))
	// #nosec G302 -- Executable mode is required for this passive lookup fixture.
	s.Require().NoError(os.Chmod(path, 0o700))
	return path
}

func (s *lookupSuite) lookup(
	environment dependencies.LookupEnvironment,
	request dependencies.LookupRequest,
) dependencies.LookupResult {
	result, err := environment.LookupExecutable(context.Background(), request)
	s.Require().NoError(err)
	s.False(result.Executed)
	for _, candidate := range result.Candidates {
		s.False(candidate.Executed)
	}
	return result
}

func (s *lookupSuite) symlink(target, link string) {
	s.T().Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == osWindows {
			s.T().Skipf("native symlink creation unavailable: %v", err)
		}
		s.Require().NoError(err)
	}
}

func (s *lookupSuite) resolved(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	s.Require().NoError(err)
	return resolved
}

func (s *lookupSuite) assertDeferred(candidate dependencies.ExecutableCandidate) {
	s.Equal(dependencies.FileDeferred, candidate.State)
	s.Equal(dependencies.LookupInspectionDeferred, candidate.ReasonCode)
	s.Empty(candidate.ResolvedPath)
	s.Nil(candidate.Identity)
	s.False(candidate.Executed)
}
