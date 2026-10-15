package runenv

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type ErrorsSuite struct{ suite.Suite }

func TestErrorsSuite(t *testing.T) { suite.Run(t, new(ErrorsSuite)) }

func (s *ErrorsSuite) TestExitCodes() {
	cleanup := NewStageError(CodeDockerWSLCleanup, "cleanup", errors.New("x"))
	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"nil":               {nil, 0},
		"config":            {ConfigError("bad"), ExitCodeUsage},
		"unsupported":       {UnsupportedProfileError("bad"), ExitCodeUsage},
		"test failed":       {NewStageError(CodeDockerWSLDaemon, "t", errors.New("x")), ExitCodeFailure},
		"report":            {NewStageError(CodeDockerWSLInstall, "r", errors.New("x")), ExitCodeFailure},
		"wrapped":           {fmt.Errorf("outer: %w", cleanup), ExitCodeFailure},
		"joined":            {errors.Join(cleanup, errors.New("other")), ExitCodeFailure},
		"cobra untyped":     {errors.New(`required flag(s) "label" not set`), ExitCodeUsage},
		"setup timeout":     {NewStageError(CodeDockerWSLSetupTimeout, "s", errors.New("x")), ExitCodeFailure},
		"oracle invalid":    {NewStageError(CodeDockerWSLOracle, "s", errors.New("x")), ExitCodeFailure},
		"endpoint mismatch": {NewStageError(CodeDockerWSLEndpoint, "s", errors.New("x")), ExitCodeFailure},
	} {
		s.Run(name, func() { s.Equal(tc.want, ExitCodeForError(tc.err)) })
	}
}

func (s *ErrorsSuite) TestErrorFormat() {
	err := NewStageError(CodeDockerWSLDaemon, "e2e-test", errors.New("test binary exited nonzero")).
		WithElapsed(1500 * time.Millisecond).
		WithExitCode(2).
		WithArtifact("/r/report.json").
		WithDetail("line1\nGH_ACCESS_TOKEN=hunter22secret\n")

	text := err.Error()
	s.True(strings.HasPrefix(
		text,
		"CI_DOCKER_WSL_DAEMON_NOT_READY: stage=e2e-test elapsed=1.5s exit=2 artifact=/r/report.json: "+
			"test binary exited nonzero\n",
	), text)
	s.Contains(text, "GH_ACCESS_TOKEN=[REDACTED]")
	s.NotContains(text, "hunter22secret")
}

func (s *ErrorsSuite) TestDetailIsBounded() {
	err := NewStageError(CodeDockerWSLDaemon, "t", nil).
		WithDetail(strings.Repeat("a", MaxDetailBytes*3) + "TAIL")
	s.LessOrEqual(len(err.Detail), MaxDetailBytes+len("[truncated] "))
	s.True(strings.HasSuffix(err.Detail, "TAIL"))
	s.True(strings.HasPrefix(err.Detail, "[truncated] "))
}

func (s *ErrorsSuite) TestCauseIsRedactedAndUnwraps() {
	cause := errors.New("login failed for GH_ACCESS_TOKEN=hunter22secret")
	err := NewStageError(CodeDockerWSLEndpoint, "p", cause)
	s.NotContains(err.Error(), "hunter22secret")
	s.ErrorIs(err, cause)
}
