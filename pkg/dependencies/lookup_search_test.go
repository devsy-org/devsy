package dependencies

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing/fstest"
	"time"
)

const (
	envPath      = "PATH"
	envPathMixed = "Path"
	toolsDocker  = `C:\Tools\Docker.exe`
)

func (s *lookupModelSuite) recordingEnvironment(
	environment LookupEnvironment,
	present map[string]bool,
) (LookupEnvironment, *[]string) {
	var seen []string
	environment.inspect = func(_ context.Context, path string) (lookupFile, error) {
		seen = append(seen, path)
		if !present[path] {
			return lookupFile{}, fs.ErrNotExist
		}
		info, err := fstest.MapFS{"f": {Mode: 0o755}}.Stat("f")
		return lookupFile{info: info, resolved: path}, err
	}
	return environment, &seen
}

func (s *lookupModelSuite) TestSearchUsesPathOrderThenReviewedLocations() {
	first, second := filepath.Join(s.root, "one"), filepath.Join(s.root, "two")
	reviewed := filepath.Join(s.root, "reviewed", testDocker)
	present := map[string]bool{
		filepath.Join(first, testDocker):  true,
		filepath.Join(second, testDocker): true,
		reviewed:                          true,
	}
	environment, seen := s.recordingEnvironment(s.environment(map[string]string{
		envPath: strings.Join([]string{second, first}, string(os.PathListSeparator)),
	}), present)

	result, err := environment.Lookup(context.Background(), LookupRequest{
		Command: testDocker, ReviewedLocations: []string{reviewed},
	})

	s.Require().NoError(err)
	s.Equal([]string{
		filepath.Join(second, testDocker), filepath.Join(first, testDocker), reviewed,
	}, *seen)
	s.Require().Len(result.Candidates, 3)
	s.Equal(LookupPATH, result.Candidates[0].Origin)
	s.Equal(LookupReviewed, result.Candidates[2].Origin)
	s.Empty(result.ReasonCode)
	s.False(result.Executed)
}

func (s *lookupModelSuite) TestSearchSkipsEmptyDotAndRelativeEntries() {
	entries := []string{"", ".", "bin", "." + string(filepath.Separator) + "bin", s.root}
	environment, seen := s.recordingEnvironment(s.environment(map[string]string{
		envPath: strings.Join(entries, string(os.PathListSeparator)),
	}), nil)

	result, err := environment.Lookup(context.Background(), LookupRequest{Command: testDocker})

	s.Require().NoError(err)
	s.Equal([]string{filepath.Join(s.root, testDocker)}, *seen)
	s.Empty(result.Candidates)
	s.Equal(LookupMissing, result.ReasonCode)
}

func (s *lookupModelSuite) TestSearchKeepsDotDotTextAfterASymlink() {
	entry := s.root + string(filepath.Separator) + "link" + string(filepath.Separator) + ".." +
		string(filepath.Separator) + "bin"
	environment, seen := s.recordingEnvironment(
		s.environment(map[string]string{envPath: entry}), nil)

	_, err := environment.Lookup(context.Background(), LookupRequest{Command: testDocker})

	s.Require().NoError(err)
	s.Equal([]string{entry + string(filepath.Separator) + testDocker}, *seen)
}

func (s *lookupModelSuite) TestSearchWithoutPathSearchesNothing() {
	for _, values := range []map[string]string{nil, {envPath: ""}} {
		environment, seen := s.recordingEnvironment(s.environment(values), nil)

		result, err := environment.Lookup(context.Background(), LookupRequest{Command: testDocker})

		s.Require().NoError(err)
		s.Empty(*seen)
		s.Equal(LookupMissing, result.ReasonCode)
	}
}

func (s *lookupModelSuite) TestSearchReportsInaccessibleNotMissing() {
	environment := s.environment(map[string]string{envPath: s.root})
	environment.inspect = func(context.Context, string) (lookupFile, error) {
		return lookupFile{}, fs.ErrPermission
	}

	result, err := environment.Lookup(context.Background(), LookupRequest{Command: testDocker})

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal(FileInaccessible, result.Candidates[0].State)
	s.Equal(LookupInaccessible, result.ReasonCode)
}

func (s *lookupModelSuite) TestSearchDefersWithTheDefaultInspector() {
	environment := s.environment(map[string]string{envPath: s.root})

	result, err := environment.Lookup(context.Background(), LookupRequest{Command: testDocker})

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal(FileDeferred, result.Candidates[0].State)
	s.Equal(LookupInspectionDeferred, result.ReasonCode)
}

func (s *lookupModelSuite) TestSearchRefusesAHugePathInsteadOfSearchingPartOfIt() {
	entries := make([]string, maxPathEntries+1)
	for i := range entries {
		entries[i] = filepath.Join(s.root, "d"+strconv.Itoa(i))
	}
	environment, seen := s.recordingEnvironment(s.environment(map[string]string{
		envPath: strings.Join(entries, string(os.PathListSeparator)),
	}), nil)

	_, err := environment.Lookup(context.Background(), LookupRequest{Command: testDocker})

	s.Require().Error(err)
	s.Empty(*seen)
}

// Comparing every candidate with every earlier one took 37 s for 20000 entries.
func (s *lookupModelSuite) TestResultAppendScalesLinearly() {
	environment := s.environment(nil)
	var result LookupResult

	start := time.Now()
	for i := range 20000 {
		path := filepath.Join(s.root, "d"+strconv.Itoa(i), testDocker)
		result.append(environment, ExecutableCandidate{LexicalPath: path, State: FileDeferred})
		result.append(environment, ExecutableCandidate{LexicalPath: path, State: FileDeferred})
	}

	s.Len(result.Candidates, 20000)
	s.Less(time.Since(start), 5*time.Second)
}

func (s *lookupModelSuite) TestWindowsSearchRefusesTooManyExtensions() {
	extensions := make([]string, maxExecutableName+1)
	for i := range extensions {
		extensions[i] = ".E" + strconv.Itoa(i)
	}
	environment, seen := s.recordingEnvironment(s.windowsEnvironment(map[string]string{
		envPath: `C:\Tools`, testPathExt: strings.Join(extensions, ";"),
	}), nil)

	_, err := environment.Lookup(context.Background(), LookupRequest{Command: testDocker})

	s.Require().Error(err)
	s.Empty(*seen)
}

func (s *lookupModelSuite) TestWindowsSearchSkipsQuotedEntriesOnEveryBuild() {
	environment, seen := s.recordingEnvironment(s.windowsEnvironment(map[string]string{
		envPath: `"C:\a;b";"C:\c"`,
	}), nil)

	result, err := environment.Lookup(context.Background(), LookupRequest{Command: testDocker})

	s.Require().NoError(err)
	s.Empty(*seen)
	s.Empty(result.Candidates)
}

func (s *lookupModelSuite) TestSearchStopsBeforeStartingOnACanceledContext() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	environment := s.environment(nil)
	for name, request := range map[string]LookupRequest{
		"automatic": {Command: testDocker},
		"explicit":  {Command: "", Explicit: true},
	} {
		_, err := environment.Lookup(ctx, request)

		s.Require().ErrorIs(err, context.Canceled, name)
	}
}

func (s *lookupModelSuite) TestSearchStopsOnCancellation() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	environment := s.environment(map[string]string{envPath: s.root})

	_, err := environment.Lookup(ctx, LookupRequest{Command: testDocker})

	s.Require().ErrorIs(err, context.Canceled)
}

func (s *lookupModelSuite) TestSearchRejectsInvalidAutomaticRequests() {
	environment := s.environment(nil)
	for name, request := range map[string]LookupRequest{
		"empty":    {},
		"path":     {Command: filepath.Join(s.root, testDocker)},
		"relative": {Command: testDocker, ReviewedLocations: []string{testDocker}},
		"origin":   {Command: testDocker, ExplicitOrigin: LookupExplicit},
	} {
		_, err := environment.Lookup(context.Background(), request)
		s.Require().Error(err, name)
	}
}

func (s *lookupModelSuite) TestWindowsSearchExpandsPathExtAndDeduplicatesCase() {
	directory := `C:\Tools`
	present := map[string]bool{
		directory + `\docker.EXE`: true, `c:\tools\docker.EXE`: true, toolsDocker: true,
	}
	environment, seen := s.recordingEnvironment(s.windowsEnvironment(map[string]string{
		envPathMixed: directory + `\;` + strings.ToLower(directory), testPathExt: ".COM;.EXE",
	}), present)

	result, err := environment.Lookup(context.Background(), LookupRequest{
		Command: testDocker, ReviewedLocations: []string{toolsDocker},
	})

	s.Require().NoError(err)
	s.Equal([]string{
		`C:\Tools\docker.COM`, `C:\Tools\docker.EXE`, `c:\tools\docker.COM`,
		`c:\tools\docker.EXE`, toolsDocker,
	}, *seen)
	s.Require().Len(result.Candidates, 1)
	s.Equal(`C:\Tools\docker.EXE`, result.Candidates[0].LexicalPath)
}

func (s *lookupModelSuite) TestWindowsSearchNeverUsesTheWorkingDirectoryOrRemotePaths() {
	entries := []string{
		"", ".", "bin", `C:bin`, `\bin`, `\\host\share`, `\\.\pipe`, `"C:\Tools"`, `C:\w\CON`,
	}
	environment, seen := s.recordingEnvironment(s.windowsEnvironment(map[string]string{
		envPath: strings.Join(entries, ";"),
	}), nil)

	result, err := environment.Lookup(context.Background(), LookupRequest{Command: testDocker})

	s.Require().NoError(err)
	s.Empty(*seen)
	s.Empty(result.Candidates)
}

func (s *lookupModelSuite) TestWindowsReviewedLocationsAreNotSuffixExpanded() {
	environment, seen := s.recordingEnvironment(s.windowsEnvironment(nil), nil)

	_, err := environment.Lookup(context.Background(), LookupRequest{
		Command: testDocker, ReviewedLocations: []string{`C:\Tools\docker`},
	})

	s.Require().NoError(err)
	s.Equal([]string{`C:\Tools\docker`}, *seen)
}

func (s *lookupModelSuite) TestExplicitLookupResolvesAgainstTheSnapshotDirectory() {
	path := filepath.Join(s.root, testDocker)
	environment, seen := s.recordingEnvironment(s.environment(nil), map[string]bool{path: true})

	result, err := environment.Lookup(context.Background(), LookupRequest{
		Command: testDocker, Explicit: true, ExplicitOrigin: LookupExplicitEnv,
	})

	s.Require().NoError(err)
	s.Equal([]string{path}, *seen)
	s.Require().Len(result.Candidates, 1)
	s.Equal(LookupExplicitEnv, result.Candidates[0].Origin)
	s.Empty(result.ReasonCode)
}

func (s *lookupModelSuite) TestExplicitLookupKeepsIntentWhenEmptyOrMissing() {
	environment, seen := s.recordingEnvironment(s.environment(nil), nil)
	for _, command := range []string{"", testDocker} {
		result, err := environment.Lookup(context.Background(), LookupRequest{
			Command: command, Explicit: true,
		})

		s.Require().NoError(err)
		s.Empty(result.Candidates)
		s.Equal(LookupInvalidPath, result.ReasonCode)
	}
	s.Equal([]string{filepath.Join(s.root, testDocker)}, *seen)
}

func (s *lookupModelSuite) TestExplicitLookupRefusesUnsafeWindowsPaths() {
	environment, seen := s.recordingEnvironment(s.windowsEnvironment(nil), nil)
	for _, command := range []string{`\\host\share\a.exe`, `C:a.exe`, `\a.exe`, `CON.exe`} {
		result, err := environment.Lookup(context.Background(), LookupRequest{
			Command: command, Explicit: true,
		})

		s.Require().NoError(err, command)
		s.Equal(LookupInvalidPath, result.ReasonCode, command)
	}
	s.Empty(*seen)
}

func (s *lookupModelSuite) TestExplicitLookupRejectsBadRequests() {
	environment := s.environment(nil)
	for name, request := range map[string]LookupRequest{
		"origin":   {Command: testDocker, Explicit: true, ExplicitOrigin: LookupPATH},
		"reviewed": {Command: testDocker, Explicit: true, ReviewedLocations: []string{s.root}},
	} {
		_, err := environment.Lookup(context.Background(), request)
		s.Require().Error(err, name)
	}
}
