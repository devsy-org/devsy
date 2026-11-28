package dependencies

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

func (s *lookupModelSuite) TestDefaultInspectionIsDeferred() {
	environment := s.environment(nil)
	path := s.file(testDocker, 0o755)

	candidate, err := environment.observe(context.Background(), path, LookupPATH)

	s.Require().NoError(err)
	s.Equal(FileDeferred, candidate.State)
	s.Equal(LookupInspectionDeferred, candidate.ReasonCode)
	s.Equal(path, candidate.LexicalPath)
	s.Equal(LookupPATH, candidate.Origin)
	s.False(candidate.Executed)
	s.Nil(candidate.Identity)
	s.NotEmpty(candidate.Platform)
}

func (s *lookupModelSuite) TestObserveRecordsExecutableFileWithIdentity() {
	path := s.file(s.name(testDocker), 0o755)
	environment := s.inspecting(s.statFile)

	candidate, err := environment.observe(context.Background(), path, LookupExplicit)

	s.Require().NoError(err)
	s.Equal(FileExecutable, candidate.State)
	s.Empty(candidate.ReasonCode)
	s.Require().NotNil(candidate.Identity)
	again, err := environment.observe(context.Background(), path, LookupExplicit)
	s.Require().NoError(err)
	s.True(candidate.Identity.SameFile(again.Identity))
	other := s.file(s.name("podman"), 0o755)
	different, err := environment.observe(context.Background(), other, LookupExplicit)
	s.Require().NoError(err)
	s.False(candidate.Identity.SameFile(different.Identity))
}

func (s *lookupModelSuite) TestObserveAppliesWindowsSuffixRule() {
	plain := s.file("plain", 0o644)
	info, err := os.Lstat(plain)
	s.Require().NoError(err)
	environment := s.windowsEnvironment(nil)
	environment.inspect = func(context.Context, string) (lookupFile, error) {
		return lookupFile{info: info}, nil
	}

	executable, err := environment.observe(context.Background(), `C:\t\docker.exe`, LookupPATH)
	s.Require().NoError(err)
	s.Equal(FileExecutable, executable.State)
	text, err := environment.observe(context.Background(), `C:\t\docker.txt`, LookupPATH)
	s.Require().NoError(err)
	s.Equal(FileNonexecutable, text.State)
	script, err := environment.observe(context.Background(), `C:\t\docker.cmd`, LookupPATH)
	s.Require().NoError(err)
	s.True(script.Script)
}

func (s *lookupModelSuite) TestObserveClassifiesFiles() {
	if runtime.GOOS == lookupWindows {
		s.T().Skip("execute bits are not used on Windows")
	}
	environment := s.inspecting(s.statFile)
	plain := s.file("plain", 0o644)
	directory := filepath.Join(s.root, "dir")
	s.Require().NoError(os.Mkdir(directory, 0o750))

	nonexecutable, err := environment.observe(context.Background(), plain, LookupPATH)
	s.Require().NoError(err)
	s.Equal(FileNonexecutable, nonexecutable.State)
	nonregular, err := environment.observe(context.Background(), directory, LookupPATH)
	s.Require().NoError(err)
	s.Equal(FileNonregular, nonregular.State)
	s.Nil(nonregular.Identity)
}

func (s *lookupModelSuite) TestSymlinkGetsNoIdentity() {
	if runtime.GOOS == lookupWindows {
		s.T().Skip("symlinks need extra rights on Windows")
	}
	target := s.file(testDocker, 0o755)
	link := filepath.Join(s.root, "link")
	s.Require().NoError(os.Symlink(target, link))
	environment := s.inspecting(s.statFile)

	candidate, err := environment.observe(context.Background(), link, LookupPATH)

	s.Require().NoError(err)
	s.Equal(FileNonregular, candidate.State)
	s.Nil(candidate.Identity)
}

func (s *lookupModelSuite) TestObserveClassifiesErrors() {
	cases := map[string]struct {
		file   lookupFile
		err    error
		state  FileState
		reason string
	}{
		"missing": {err: fs.ErrNotExist, state: FileMissing},
		"not a dir": {
			err:   &fs.PathError{Op: "lstat", Path: "/x", Err: syscall.ENOTDIR},
			state: FileMissing,
		},
		"broken link": {
			file:  lookupFile{symlink: true},
			err:   fs.ErrNotExist,
			state: FileBrokenLink,
		},
		"inaccessible": {
			err: fs.ErrPermission, state: FileInaccessible, reason: LookupInaccessible,
		},
		"other":    {err: errors.New("boom"), state: FileInaccessible},
		"deferred": {err: errLookupDeferred, state: FileDeferred},
		"no info":  {state: FileInaccessible},
	}
	for name, test := range cases {
		s.Run(name, func() {
			environment := s.inspecting(func(context.Context, string) (lookupFile, error) {
				return test.file, test.err
			})

			candidate, err := environment.observe(context.Background(), "/x/tool", LookupPATH)

			s.Require().NoError(err)
			s.Equal(test.state, candidate.State)
			s.Equal(test.file.symlink, candidate.IsSymlink)
			if test.reason != "" {
				s.Equal(test.reason, candidate.ReasonCode)
			}
		})
	}
}

func (s *lookupModelSuite) TestObserveStopsOnCancellation() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	environment := s.inspecting(func(context.Context, string) (lookupFile, error) {
		s.Fail("inspection must not start after cancellation")
		return lookupFile{}, nil
	})

	candidate, err := environment.observe(ctx, "/x/tool", LookupPATH)

	s.Require().ErrorIs(err, context.Canceled)
	s.Empty(candidate.LexicalPath)
}

func (s *lookupModelSuite) TestObserveCancellationDuringInspectionWins() {
	ctx, cancel := context.WithCancel(context.Background())
	environment := s.inspecting(func(context.Context, string) (lookupFile, error) {
		cancel()
		return lookupFile{}, nil
	})

	_, err := environment.observe(ctx, "/x/tool", LookupPATH)

	s.Require().ErrorIs(err, context.Canceled)
}

func (s *lookupModelSuite) TestLookupCanceled() {
	s.True(lookupCanceled(context.Canceled))
	s.True(lookupCanceled(context.DeadlineExceeded))
	s.False(lookupCanceled(errors.New("other")))
	s.False(lookupCanceled(nil))
}

func (s *lookupModelSuite) TestLauncherHasReasonAndNoIdentity() {
	shims := filepath.Join(s.root, "shims")
	s.Require().NoError(os.Mkdir(shims, 0o750))
	path := s.file(filepath.Join("shims", s.name(testDocker)), 0o755)
	environment := s.inspecting(s.statFile)
	environment.values = map[string]string{"MISE_DATA_DIR": s.root}

	candidate, err := environment.observe(context.Background(), path, LookupPATH)

	s.Require().NoError(err)
	s.True(candidate.Launcher)
	s.Equal(LookupIndirectLauncher, candidate.ReasonCode)
	s.Nil(candidate.Identity)
}

func (s *lookupModelSuite) TestSameFileHandlesNil() {
	var none *FileObservation
	s.False(none.SameFile(none))
	s.False(none.SameFile(&FileObservation{}))
}

func (s *lookupModelSuite) TestDeferredCandidateKeepsLexicalEvidenceOnly() {
	candidate := s.environment(nil).deferredCandidate("tool.cmd", LookupReviewed)

	s.Equal(FileDeferred, candidate.State)
	s.Equal(LookupInspectionDeferred, candidate.ReasonCode)
	s.True(candidate.Script)
	s.Empty(candidate.ResolvedPath)
	s.Nil(candidate.Identity)
	s.False(candidate.Executed)
}

func (s *lookupModelSuite) TestResultSkipsMissingAndDuplicates() {
	var result LookupResult
	environment := s.environment(nil)
	result.append(environment, ExecutableCandidate{LexicalPath: "/a", State: FileMissing})
	result.append(environment, ExecutableCandidate{LexicalPath: "/b", State: FileNonexecutable})
	result.append(environment, ExecutableCandidate{LexicalPath: "/b", State: FileExecutable})

	s.Len(result.Candidates, 1)
	s.Equal(FileNonexecutable, result.Candidates[0].State)
}

func (s *lookupModelSuite) TestResultDeduplicatesByPlatformNameRules() {
	windows := s.windowsEnvironment(nil)
	var result LookupResult
	for _, path := range []string{`C:\Tools\a.exe`, `c:\tools\A.EXE`, `C:\Tools\\a.exe`, `C:/Tools/a.exe`} {
		result.append(windows, ExecutableCandidate{LexicalPath: path, State: FileExecutable})
	}
	s.Len(result.Candidates, 1)

	for _, path := range []string{`C:\Tools\b.exe`, `C:\Other\a.exe`} {
		result.append(windows, ExecutableCandidate{LexicalPath: path, State: FileExecutable})
	}
	s.Len(result.Candidates, 3)
}

func (s *lookupModelSuite) TestResultKeepsDotComponentPathsDistinct() {
	environment := s.environment(nil)
	var result LookupResult
	for _, path := range []string{"/a/../b/tool", "/b/tool", "/a/./b/tool"} {
		result.append(environment, ExecutableCandidate{LexicalPath: path, State: FileExecutable})
	}

	s.Len(result.Candidates, 3)
}

func (s *lookupModelSuite) TestResultKeepsCaseVariantsOnCaseSensitivePlatform() {
	environment := s.environment(nil)
	if environment.caseInsensitive() {
		s.T().Skip("platform ignores case")
	}
	var result LookupResult
	result.append(environment, ExecutableCandidate{LexicalPath: "/t/a", State: FileExecutable})
	result.append(environment, ExecutableCandidate{LexicalPath: "/t/A", State: FileExecutable})

	s.Len(result.Candidates, 2)
}

func (s *lookupModelSuite) TestObserveRefusesPathsBeforeInspection() {
	environment := s.windowsEnvironment(nil)
	environment.inspect = func(context.Context, string) (lookupFile, error) {
		s.Fail("inspection must not start for a refused path")
		return lookupFile{}, nil
	}
	for _, path := range []string{
		"", `tool.exe`, `\\host\share\a.exe`, `\\.\pipe\a`, `C:a.exe`,
		deviceName, `C:\w\a.exe:s`, "C:\\w\\a\x00.exe",
	} {
		candidate, err := environment.observe(context.Background(), path, LookupExplicit)

		s.Require().NoError(err, path)
		s.Equal(FileInaccessible, candidate.State, path)
		s.Equal(LookupInvalidPath, candidate.ReasonCode, path)
		s.Nil(candidate.Identity, path)
	}
}

func (s *lookupModelSuite) TestObserveRefusesRelativePathOnThisPlatform() {
	environment := s.inspecting(func(context.Context, string) (lookupFile, error) {
		s.Fail("inspection must not start for a refused path")
		return lookupFile{}, nil
	})

	candidate, err := environment.observe(context.Background(), "tool", LookupPATH)

	s.Require().NoError(err)
	s.Equal(LookupInvalidPath, candidate.ReasonCode)
}

func (s *lookupModelSuite) TestObserveJudgesWindowsLinkTargetSuffix() {
	plain := s.file("plain", 0o644)
	info, err := os.Lstat(plain)
	s.Require().NoError(err)
	environment := s.windowsEnvironment(nil)
	resolved := `C:\w\data.txt`
	environment.inspect = func(context.Context, string) (lookupFile, error) {
		return lookupFile{info: info, resolved: resolved, symlink: true}, nil
	}

	candidate, err := environment.observe(context.Background(), `C:\w\link.exe`, LookupPATH)
	s.Require().NoError(err)
	s.Equal(FileNonexecutable, candidate.State)

	resolved = `C:\w\tool.exe`
	candidate, err = environment.observe(context.Background(), `C:\w\link.exe`, LookupPATH)
	s.Require().NoError(err)
	s.Equal(FileExecutable, candidate.State)
}

func (s *lookupModelSuite) TestResultReasonCodes() {
	usable := ExecutableCandidate{State: FileExecutable}
	launcher := ExecutableCandidate{State: FileExecutable, ReasonCode: LookupIndirectLauncher}
	deferred := ExecutableCandidate{State: FileDeferred, ReasonCode: LookupInspectionDeferred}
	cases := map[string]struct {
		candidates []ExecutableCandidate
		explicit   bool
		reason     string
	}{
		"none":          {reason: LookupMissing},
		"explicit none": {explicit: true, reason: LookupInvalidPath},
		"nonexecutable": {
			candidates: []ExecutableCandidate{{State: FileNonexecutable}},
			reason:     LookupMissing,
		},
		"inaccessible": {
			candidates: []ExecutableCandidate{
				{State: FileNonexecutable},
				{State: FileInaccessible, ReasonCode: LookupInaccessible},
			},
			reason: LookupInaccessible,
		},
		"explicit inaccessible": {
			candidates: []ExecutableCandidate{
				{State: FileInaccessible, ReasonCode: LookupInaccessible},
			},
			explicit: true,
			reason:   LookupInaccessible,
		},
		"usable beats inaccessible": {
			candidates: []ExecutableCandidate{
				{State: FileInaccessible, ReasonCode: LookupInaccessible}, usable,
			},
		},
		"executable": {candidates: []ExecutableCandidate{usable}},
		"launcher": {
			candidates: []ExecutableCandidate{launcher},
			reason:     LookupIndirectLauncher,
		},
		"deferred": {
			candidates: []ExecutableCandidate{deferred},
			reason:     LookupInspectionDeferred,
		},
		"first usable":    {candidates: []ExecutableCandidate{{State: FileNonregular}, usable}},
		"explicit usable": {candidates: []ExecutableCandidate{usable}, explicit: true},
	}
	for name, test := range cases {
		s.Run(name, func() {
			result := LookupResult{Candidates: test.candidates}
			result.finish(test.explicit)
			s.Equal(test.reason, result.ReasonCode)
			s.False(result.Executed)
		})
	}
}

func (s *lookupModelSuite) TestAutomaticLookupValidation() {
	environment := s.environment(nil)
	reviewed := filepath.Join(s.root, testDocker)
	s.Require().NoError(environment.validateAutomaticLookup(LookupRequest{
		Command: testDocker, ReviewedLocations: []string{reviewed},
	}))
	invalid := map[string]LookupRequest{
		"explicit origin":   {Command: testDocker, ExplicitOrigin: LookupExplicit},
		"explicit":          {Command: testDocker, Explicit: true},
		"empty command":     {},
		"nul command":       {Command: "dock\x00er"},
		"path command":      {Command: filepath.Join("bin", testDocker)},
		"relative reviewed": {Command: testDocker, ReviewedLocations: []string{testDocker}},
		"nul reviewed":      {Command: testDocker, ReviewedLocations: []string{reviewed + "\x00"}},
	}
	for name, request := range invalid {
		s.Run(name, func() { s.Require().Error(environment.validateAutomaticLookup(request)) })
	}
}

func (s *lookupModelSuite) TestWindowsAutomaticLookupValidation() {
	environment := s.windowsEnvironment(nil)
	s.Require().NoError(environment.validateAutomaticLookup(LookupRequest{
		Command:           testDocker,
		ReviewedLocations: []string{`C:\Tools\docker.exe`, `\\?\C:\Tools\docker.exe`},
	}))
	for _, location := range []string{
		`\\host\share\docker.exe`, `\\.\pipe\docker`, `\\?\UNC\host\share\docker.exe`, `C:docker.exe`, `\docker.exe`,
	} {
		err := environment.validateAutomaticLookup(LookupRequest{
			Command: testDocker, ReviewedLocations: []string{location},
		})
		s.Require().Error(err, location)
	}
	for _, command := range []string{"CON", "docker.", "dock*r", `C:docker`} {
		s.Require().
			Error(environment.validateAutomaticLookup(LookupRequest{Command: command}), command)
	}
}

func (s *lookupModelSuite) inspecting(
	inspect func(context.Context, string) (lookupFile, error),
) LookupEnvironment {
	environment := s.environment(nil)
	environment.inspect = inspect
	return environment
}

func (s *lookupModelSuite) statFile(_ context.Context, path string) (lookupFile, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return lookupFile{}, err
	}
	return lookupFile{info: info, resolved: path}, nil
}

func (s *lookupModelSuite) name(base string) string {
	if runtime.GOOS == lookupWindows {
		return base + ".exe"
	}
	return base
}

func (s *lookupModelSuite) file(name string, mode os.FileMode) string {
	path := filepath.Join(s.root, name)
	s.Require().NoError(os.WriteFile(path, nil, 0o600))
	s.Require().NoError(os.Chmod(path, mode))
	return path
}
