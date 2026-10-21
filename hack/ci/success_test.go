package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

const (
	goTrue       = `{"result":"success","outputs":{"go":"true"}}`
	goFalse      = `{"result":"success","outputs":{"go":"false"}}`
	okJob        = `{"result":"success","outputs":{}}`
	skippedJob   = `{"result":"skipped","outputs":{}}`
	failedJob    = `{"result":"failure","outputs":{}}`
	cancelledJob = `{"result":"cancelled","outputs":{}}`
)

// successResult is the outcome of one `ci success` invocation.
type successResult struct {
	code   int
	stdout string
	stderr string
}

type successSuite struct {
	suite.Suite
}

func TestSuccessSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(successSuite))
}

// needsObject builds a toJSON(needs) object from job id and raw JSON pairs, in the given order.
func needsObject(jobs ...string) string {
	return "{" + strings.Join(jobs, ",") + "}"
}

func entry(name, body string) string {
	return `"` + name + `":` + body
}

func (s *successSuite) TestAllSuccessPasses() {
	needs := needsObject(entry("changes", goTrue), entry("lint", okJob), entry("unit-tests", okJob))

	res := s.runSuccess(needs)

	s.Equal(0, res.code)
	s.Equal("all 3 required jobs passed or were legitimately skipped\n", res.stdout)
	s.Empty(res.stderr)
}

func (s *successSuite) TestSkippedWithoutGoChangesIsAllowed() {
	needs := needsObject(
		entry("changes", goFalse),
		entry("unit-tests", skippedJob),
		entry("lint", okJob),
	)

	res := s.runSuccess(needs)

	s.Equal(0, res.code)
	s.Equal("all 3 required jobs passed or were legitimately skipped\n", res.stdout)
}

func (s *successSuite) TestSkippedWithGoChangesIsRejected() {
	needs := needsObject(entry("changes", goTrue), entry("unit-tests", skippedJob))

	res := s.runSuccess(needs)

	s.Equal(1, res.code)
	s.Contains(res.stdout, "::error::unit-tests ended with result=skipped unexpectedly\n")
	s.NotContains(res.stdout, "all ")
}

func (s *successSuite) TestAlwaysRunSkippedIsRejectedWithoutGoChanges() {
	needs := needsObject(
		entry("changes", goFalse),
		entry("lint", skippedJob),
		entry("precommit", skippedJob),
	)

	res := s.runSuccess(needs, "--always-run", " lint, precommit ,")

	s.Equal(1, res.code)
	s.Contains(res.stdout, "::error::lint ended with result=skipped unexpectedly\n")
	s.Contains(res.stdout, "::error::precommit ended with result=skipped unexpectedly\n")
}

func (s *successSuite) TestAlwaysRunListIsPerJob() {
	needs := needsObject(
		entry("changes", goFalse),
		entry("lint", skippedJob),
		entry("unit-tests", skippedJob),
	)

	res := s.runSuccess(needs, "--always-run", "lint")

	s.Equal(1, res.code)
	s.Contains(res.stdout, "::error::lint ended with result=skipped unexpectedly\n")
	s.NotContains(res.stdout, "unit-tests")
}

func (s *successSuite) TestFailureAndCancelledAreRejected() {
	needs := needsObject(
		entry("changes", goTrue),
		entry("lint", failedJob),
		entry("unit-tests", cancelledJob),
		entry("build-cli", `{"result":"","outputs":{}}`),
	)

	res := s.runSuccess(needs)

	s.Equal(1, res.code)
	s.Contains(res.stdout, "::error::build-cli ended with result=\n")
	s.Contains(res.stdout, "::error::lint ended with result=failure\n")
	s.Contains(res.stdout, "::error::unit-tests ended with result=cancelled\n")
	s.NotContains(res.stdout, "all ")
}

func (s *successSuite) TestMissingChangesJobFails() {
	needs := needsObject(entry("lint", okJob))

	res := s.runSuccess(needs)

	s.Equal(1, res.code)
	s.Contains(res.stdout, "::error::changes job missing from NEEDS_JSON\n")
}

func (s *successSuite) TestMissingChangesRejectsSkippedJobs() {
	needs := needsObject(entry("lint", okJob), entry("unit-tests", skippedJob))

	res := s.runSuccess(needs)

	s.Equal(1, res.code)
	s.Contains(res.stdout, "::error::changes job missing from NEEDS_JSON\n")
	s.Contains(res.stdout, "::error::unit-tests ended with result=skipped unexpectedly\n")
}

func (s *successSuite) TestEmptyOrInvalidNeedsFails() {
	tests := []struct {
		name    string
		needs   string
		message string
	}{
		{
			name:    "empty variable",
			needs:   "",
			message: "::error::needs variable is empty: NEEDS_JSON\n",
		},
		{
			name:    "invalid JSON",
			needs:   "{not json",
			message: "::error::needs variable is not valid JSON: NEEDS_JSON",
		},
		{
			name:    "empty object",
			needs:   "{}",
			message: "::error::needs variable has no jobs: NEEDS_JSON\n",
		},
		{name: "null", needs: "null", message: "::error::needs variable has no jobs: NEEDS_JSON\n"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			res := s.runSuccess(tt.needs)

			s.Equal(1, res.code)
			s.Contains(res.stdout, tt.message)
			s.NotContains(res.stdout, "all ")
		})
	}
}

func (s *successSuite) TestUnsetNeedsVariableNameFails() {
	needs := needsObject(entry("changes", goTrue), entry("lint", okJob))

	res := s.runSuccess(needs, "--needs-env", "MISSING_VAR")

	s.Equal(1, res.code)
	s.Contains(res.stdout, "::error::needs variable is empty: MISSING_VAR\n")
}

func (s *successSuite) TestNeedsEnvFlagSelectsVariable() {
	var stdout, stderr bytes.Buffer
	check := successCheck{
		getenv: func(name string) string {
			if name == "CUSTOM_NEEDS" {
				return needsObject(entry("changes", goTrue), entry("lint", okJob))
			}
			return ""
		},
		stdout: &stdout,
		stderr: &stderr,
	}

	code := check.run([]string{"--needs-env", "CUSTOM_NEEDS"})

	s.Equal(0, code)
	s.Equal("all 2 required jobs passed or were legitimately skipped\n", stdout.String())
}

func (s *successSuite) TestOutputIsSortedByJobID() {
	needs := needsObject(
		entry("zeta", failedJob),
		entry("changes", goTrue),
		entry("mid", cancelledJob),
		entry("alpha", skippedJob),
	)

	res := s.runSuccess(needs)

	s.Equal(1, res.code)
	s.Equal(
		"::error::alpha ended with result=skipped unexpectedly\n"+
			"::error::mid ended with result=cancelled\n"+
			"::error::zeta ended with result=failure\n",
		res.stdout,
	)
}

func (s *successSuite) TestFailureDoesNotHideOtherBadJobs() {
	needs := needsObject(
		entry("changes", goTrue),
		entry("alpha", failedJob),
		entry("beta", okJob),
		entry("zeta", skippedJob),
	)

	res := s.runSuccess(needs)

	s.Equal(1, res.code)
	s.Contains(res.stdout, "::error::alpha ended with result=failure\n")
	s.Contains(res.stdout, "::error::zeta ended with result=skipped unexpectedly\n")
	s.NotContains(res.stdout, "beta")
}

func (s *successSuite) TestUnexpectedArgumentsIsUsageError() {
	needs := needsObject(entry("changes", goTrue))

	res := s.runSuccess(needs, "extra")

	s.Equal(2, res.code)
	s.Contains(res.stderr, "unexpected arguments: extra")
	s.Empty(res.stdout)
}

func (s *successSuite) TestUnknownFlagIsUsageError() {
	needs := needsObject(entry("changes", goTrue))

	res := s.runSuccess(needs, "--bogus")

	s.Equal(2, res.code)
	s.Contains(res.stderr, "flag provided but not defined")
	s.Empty(res.stdout)
}

// runSuccess runs `ci success` with args, serving needs from the NEEDS_JSON variable only.
func (s *successSuite) runSuccess(needs string, args ...string) successResult {
	var stdout, stderr bytes.Buffer
	check := successCheck{
		getenv: func(name string) string {
			if name == defaultNeedsEnv {
				return needs
			}
			return ""
		},
		stdout: &stdout,
		stderr: &stderr,
	}
	return successResult{code: check.run(args), stdout: stdout.String(), stderr: stderr.String()}
}
