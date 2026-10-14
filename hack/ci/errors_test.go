package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/suite"
)

type ErrorsSuite struct{ suite.Suite }

func TestErrorsSuite(t *testing.T) { suite.Run(t, new(ErrorsSuite)) }

func (s *ErrorsSuite) TestExitCodes() {
	cleanup := newStageError(CodeDockerWSLCleanup, "cleanup", errors.New("x"))
	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"nil":               {nil, 0},
		"config":            {configError("bad"), exitCodeUsage},
		"unsupported":       {unsupportedProfileError("bad"), exitCodeUsage},
		"test failed":       {newStageError(CodeE2ETestFailed, "t", errors.New("x")), exitCodeFailure},
		"report":            {newStageError(CodeE2EReportInvalid, "r", errors.New("x")), exitCodeFailure},
		"wrapped":           {fmt.Errorf("outer: %w", cleanup), exitCodeFailure},
		"joined":            {errors.Join(cleanup, errors.New("other")), exitCodeFailure},
		"cobra untyped":     {errors.New(`required flag(s) "label" not set`), exitCodeUsage},
		"setup timeout":     {newStageError(CodeDockerWSLSetupTimeout, "s", errors.New("x")), exitCodeFailure},
		"oracle invalid":    {newStageError(CodeDockerWSLOracle, "s", errors.New("x")), exitCodeFailure},
		"endpoint mismatch": {newStageError(CodeDockerWSLEndpoint, "s", errors.New("x")), exitCodeFailure},
	} {
		s.Run(name, func() { s.Equal(tc.want, exitCodeForError(tc.err)) })
	}
}

func (s *ErrorsSuite) TestErrorFormat() {
	err := newStageError(CodeE2ETestFailed, "e2e-test", errors.New("test binary exited nonzero")).
		WithElapsed(1500 * time.Millisecond).
		WithExitCode(2).
		WithArtifact("/r/report.json").
		WithDetail("line1\nGH_ACCESS_TOKEN=hunter22secret\n")

	text := err.Error()
	s.True(strings.HasPrefix(text,
		"CI_E2E_TEST_FAILED: stage=e2e-test elapsed=1.5s exit=2 artifact=/r/report.json: "+
			"test binary exited nonzero\n"), text)
	s.Contains(text, "GH_ACCESS_TOKEN=[REDACTED]")
	s.NotContains(text, "hunter22secret")
}

func (s *ErrorsSuite) TestDetailIsBounded() {
	err := newStageError(CodeE2ETestFailed, "t", nil).
		WithDetail(strings.Repeat("a", maxDetailBytes*3) + "TAIL")
	s.LessOrEqual(len(err.Detail), maxDetailBytes+len("[truncated] "))
	s.True(strings.HasSuffix(err.Detail, "TAIL"))
	s.True(strings.HasPrefix(err.Detail, "[truncated] "))
}

func (s *ErrorsSuite) TestCauseIsRedactedAndUnwraps() {
	cause := errors.New("login failed for GH_ACCESS_TOKEN=hunter22secret")
	err := newStageError(CodeE2EPreflightFailed, "p", cause)
	s.NotContains(err.Error(), "hunter22secret")
	s.ErrorIs(err, cause)
}

func (s *ErrorsSuite) TestRootCommandSurface() {
	saved := extraCommands
	s.T().Cleanup(func() { extraCommands = saved })
	extraCommands = append(slices.Clone(saved), func(*Dependencies) *cobra.Command {
		return &cobra.Command{Use: "seam-probe", RunE: func(*cobra.Command, []string) error {
			return newStageError(CodeDockerWSLCleanup, "cleanup", errors.New("seam works"))
		}}
	})
	deps := newTestRig(goosLinux, nil).Dependencies
	root := newRootCommand(deps)

	names := []string{}
	for _, c := range root.Commands() {
		names = append(names, c.Name())
	}
	s.Contains(names, "e2e")
	s.Contains(names, "docker-wsl", "the real docker-wsl command registers through the seam")
	s.Contains(names, "seam-probe")

	root.SetArgs([]string{"seam-probe"})
	err := root.Execute()
	s.Equal([]Code{CodeDockerWSLCleanup}, codesOf(err))
}

func (s *ErrorsSuite) TestRunFlagsRequireLabelAndReport() {
	deps := newTestRig(goosLinux, nil).Dependencies
	root := newRootCommand(deps)
	root.SetArgs([]string{"e2e", "run"})
	root.SetOut(&strings.Builder{})
	root.SetErr(&strings.Builder{})
	err := root.Execute()
	s.Require().Error(err)
	s.Equal(exitCodeUsage, exitCodeForError(err))
	s.Contains(err.Error(), "label")
}

func (s *ErrorsSuite) TestRunFlagDefaults() {
	deps := newTestRig(goosLinux, nil).Dependencies
	run, _, err := newRootCommand(deps).Find([]string{"e2e", "run"})
	s.Require().NoError(err)
	for flag, want := range map[string]string{
		"profile": profileLegacy, "podman-mode": podmanModeNone,
		"timeout": "25m0s", "flake-attempts": "1", "label": "", "report": "", "binary": "",
	} {
		f := run.Flags().Lookup(flag)
		s.Require().NotNil(f, flag)
		s.Equal(want, f.DefValue, flag)
	}
}
