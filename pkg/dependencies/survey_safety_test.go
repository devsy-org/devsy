package dependencies

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/stretchr/testify/require"
)

func (s *surveySuite) TestURIOptionsAreRejectedBeforeInspection() {
	for _, value := range []string{
		"https://user:" + surveyTestSecret + "@host/docker",
		"ssh://user:" + surveyTestSecret + "@host/docker",
		"file://user:" + surveyTestSecret + "@host/docker",
	} {
		request := s.request("docker", "")
		request.Options[surveyTestOption] = SurveyOption{Value: value, UserProvided: true}
		request.Environment.inspect = func(context.Context, string) (lookupFile, error) {
			s.FailNow("URI input must not reach filesystem metadata")
			return lookupFile{}, nil
		}
		dependencytest.Preserve(s.T(), s.root)
		result := s.survey(request)
		item := result.Requirements[0]
		s.Equal(LookupInvalidPath, item.ReasonCode)
		s.Equal(surveyUnknown, item.Availability)
		s.Empty(item.Candidates)
		encoded, err := json.Marshal(result)
		s.Require().NoError(err)
		s.NotContains(string(encoded), surveyTestSecret)
	}
}

func (s *surveySuite) TestDeferredExplicitAndShimNeverFallback() {
	fallback := s.executable("fallback/docker")
	shim := s.executable("home/.local/share/mise/shims/docker")
	for _, deferred := range []bool{false, true} {
		request := s.request(
			"docker",
			filepath.Dir(shim)+string(filepath.ListSeparator)+filepath.Dir(fallback),
		)
		request.Options[surveyTestOption] = SurveyOption{Value: toolDocker, UserProvided: true}
		request.ReviewedLocations[toolDocker] = []string{fallback}
		if deferred {
			request.Environment.inspect = func(ctx context.Context, path string) (lookupFile, error) {
				if path == shim {
					return lookupFile{}, errLookupDeferred
				}
				return inspectLookupFile(ctx, path)
			}
		}
		dependencytest.Preserve(s.T(), s.root)
		item := s.survey(request).Requirements[0]
		s.Require().Len(item.Candidates, 1)
		candidate := item.Candidates[0]
		s.Equal(shim, candidate.LexicalPath)
		s.Nil(candidate.Identity)
		if deferred {
			s.Equal(surveyUnknown, item.Availability)
			s.Equal(surveyPolicyBlocked, item.ReasonCode)
			s.Equal(surveyPolicyBlocked, candidate.ReasonCode)
			s.Equal(FileDeferred, candidate.State)
			s.Empty(candidate.ResolvedPath)
		} else {
			s.Equal(surveyAvailable, item.Availability)
			s.Equal(LookupIndirectLauncher, item.ReasonCode)
			s.True(candidate.Launcher)
		}
	}
}

func (s *surveySuite) TestFileStatesRemainDistinct() {
	if runtime.GOOS == lookupWindows {
		s.T().Skip("Unix execute bits and symlinks; native Windows safety is covered separately")
	}
	plain := filepath.Join(s.root, "plain")
	s.Require().NoError(os.WriteFile(plain, []byte("not executable"), 0o600))
	directory := filepath.Join(s.root, "directory")
	s.Require().NoError(os.Mkdir(directory, 0o700))
	missing := filepath.Join(s.root, "missing")
	broken := filepath.Join(s.root, "broken")
	s.Require().NoError(os.Symlink(missing, broken))
	for _, test := range []struct {
		path         string
		state        FileState
		availability string
	}{
		{plain, FileNonexecutable, surveyIncomplete},
		{directory, FileNonregular, surveyIncomplete},
		{missing, FileMissing, surveyMissing},
		{broken, FileBrokenLink, surveyIncomplete},
	} {
		request := s.request("docker", "")
		request.Options[surveyTestOption] = SurveyOption{Value: test.path, UserProvided: true}
		item := s.survey(request).Requirements[0]
		s.Equal(LookupInvalidPath, item.ReasonCode)
		s.Equal(test.availability, item.Availability)
		s.Require().Len(item.Candidates, 1)
		s.Equal(test.state, item.Candidates[0].State)
	}
}

func (s *surveySuite) TestCanceledAndDeadlineReturnNoPartialResult() {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		request := s.externalRequest(toolMicrosandbox, executionMicrosandbox)
		path := s.executable("tools/msb")
		var err error
		request.Environment, err = NewLookupEnvironment(
			map[string]string{surveyTestPATH: filepath.Dir(path)},
			s.root,
		)
		s.Require().NoError(err)
		request.Environment.inspect = func(ctx context.Context, path string) (lookupFile, error) {
			if strings.EqualFold(filepath.Base(path), "docker") ||
				strings.EqualFold(filepath.Base(path), "docker.exe") {
				return lookupFile{}, cause
			}
			return inspectLookupFile(ctx, path)
		}
		result, err := s.catalog.Survey(context.Background(), request)
		s.ErrorIs(err, cause)
		s.Equal(SurveyResult{}, result)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	deadline, expire := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expire()
	for _, ctx := range []context.Context{canceled, deadline} {
		result, err := s.catalog.Survey(ctx, s.request("docker", ""))
		s.ErrorIs(err, ctx.Err())
		s.Equal(SurveyResult{}, result)
	}
}

func (s *surveySuite) TestAutomaticUnusableAndInaccessibleAreNotMissing() {
	if runtime.GOOS == lookupWindows {
		s.T().Skip("Unix execute-bit observation")
	}
	path := s.executable("tools/docker")
	s.Require().NoError(os.Chmod(path, 0o600))
	request := s.request("docker", filepath.Dir(path))
	item := s.survey(request).Requirements[0]
	s.Equal(surveyIncomplete, item.Availability)
	s.Equal("dependency_incomplete", item.ReasonCode)
	s.Require().Len(item.Candidates, 1)
	s.Equal(FileNonexecutable, item.Candidates[0].State)
	request.Environment.inspect = func(ctx context.Context, inspected string) (lookupFile, error) {
		if inspected == path {
			return lookupFile{}, fs.ErrPermission
		}
		return inspectLookupFile(ctx, inspected)
	}
	item = s.survey(request).Requirements[0]
	s.Equal(surveyUnknown, item.Availability)
	s.Equal(surveyPolicyBlocked, item.ReasonCode)
	s.Require().Len(item.Candidates, 1)
	s.Equal(FileInaccessible, item.Candidates[0].State)
}

func (s *surveySuite) TestContextCancellationDuringInspectionDiscardsEvidence() {
	path := s.executable("tools/docker")
	request := s.request("docker", filepath.Dir(path))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request.Environment.inspect = func(ctx context.Context, path string) (lookupFile, error) {
		observed, err := inspectLookupFile(ctx, path)
		cancel()
		return observed, err
	}
	result, err := s.catalog.Survey(ctx, request)
	s.ErrorIs(err, context.Canceled)
	s.Equal(SurveyResult{}, result)
}

func (s *surveySuite) TestInvalidInputsDoNotEchoValues() {
	for _, mutate := range []func(*SurveyRequest){
		func(r *SurveyRequest) { r.Contract.AdapterVersion = surveyTestSecret },
		func(r *SurveyRequest) { r.Contract.ManifestSHA256 = surveyTestSecret },
		func(r *SurveyRequest) { r.Options[surveyTestOption] = SurveyOption{Origin: surveyTestSecret} },
		func(r *SurveyRequest) { r.Environment = LookupEnvironment{} },
		func(r *SurveyRequest) { r.Host.GOOS = surveyTestSecret },
		func(r *SurveyRequest) { r.Host.GOARCH = surveyTestSecret },
	} {
		request := s.request("docker", "")
		mutate(&request)
		result, err := s.catalog.Survey(context.Background(), request)
		s.Require().Error(err)
		s.NotContains(err.Error(), surveyTestSecret)
		s.Equal(SurveyResult{}, result)
	}
	var unloaded *Catalog
	result, err := unloaded.Survey(context.Background(), s.request("docker", ""))
	s.Error(err)
	s.Equal(SurveyResult{}, result)
}

func (s *surveySuite) TestCWDAndAmbientEnvironmentAreExcluded() {
	cwd := s.executable("docker")
	ambient := s.executable("ambient/docker")
	s.T().Setenv(surveyTestPATH, filepath.Dir(ambient))
	request := s.request("docker", "."+string(filepath.ListSeparator)+filepath.Dir(cwd))
	dependencytest.Preserve(s.T(), s.root)
	item := s.survey(request).Requirements[0]
	s.Equal(surveyMissing, item.Availability)
	s.Empty(item.Candidates)
}

func TestSurveyHasNoEffectfulImports(t *testing.T) {
	files, err := filepath.Glob("survey*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, imported := range file.Imports {
			name, err := strconv.Unquote(imported.Path.Value)
			require.NoError(t, err)
			for _, forbidden := range []string{
				"os/exec", "net", "github.com/devsy-org/devsy/pkg/workspace",
				"github.com/devsy-org/devsy/pkg/driver", "github.com/devsy-org/devsy/pkg/provider",
				"github.com/devsy-org/devsy/pkg/config", "github.com/devsy-org/devsy/cmd",
			} {
				require.NotEqual(t, forbidden, name, path)
				require.False(t, strings.HasPrefix(name, forbidden+"/"), path)
			}
		}
	}
}
