package main

import (
	"context"
	"strings"

	"github.com/stretchr/testify/require"
)

type fakeRuns struct {
	conclusions []string
	err         error
}

func (f fakeRuns) Conclusions(context.Context, string) ([]string, error) {
	return f.conclusions, f.err
}

func (s *scopeSuite) TestPreviousRunDecidesWhetherALightPushStaysLight() {
	tests := []struct {
		name  string
		runs  fakeRuns
		light bool
	}{
		{"previous run passed", fakeRuns{conclusions: []string{resultSuccess}}, true},
		{"previous run failed", fakeRuns{conclusions: []string{resultFailure}}, true},
		{
			"cancelled then rerun passed",
			fakeRuns{conclusions: []string{cancelledState, resultSuccess}},
			true,
		},
		{"replaced in the queue", fakeRuns{conclusions: []string{cancelledState}}, false},
		{"still running", fakeRuns{conclusions: []string{"none"}}, false},
		{"timed out", fakeRuns{conclusions: []string{"timed_out"}}, false},
		{"no run found", fakeRuns{}, false},
		{"cannot list runs", fakeRuns{err: errScopeTest}, false},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			light := scopeDecision{Light: true, Reason: "docs"}
			push := pushRange{scopeTestBefore, scopeTestSHA}

			decision := requirePreviousRun(context.Background(), tt.runs, push, light)

			s.Equal(tt.light, decision.Light, decision.Reason)
			s.NotEmpty(decision.Reason)
		})
	}
}

func (s *scopeSuite) TestPreviousRunIsNotReadForAFullPush() {
	full := fullScope("go file")

	decision := requirePreviousRun(
		context.Background(), fakeRuns{err: errScopeTest}, pushRange{}, full,
	)

	s.Equal(full, decision)
}

func (s *scopeSuite) TestGitHubRunHistoryReadsPushRunsOfTheCommit() {
	runner := &scriptedRunner{outputs: map[string]string{"api": "cancelled\nnone\nsuccess\n"}}
	history := ghRunHistory{runner: runner, repo: "devsy-org/devsy"}

	conclusions, err := history.Conclusions(context.Background(), scopeTestBefore)

	s.Require().NoError(err)
	s.Equal([]string{cancelledState, "none", resultSuccess}, conclusions)
	call := runner.calls[0]
	prefix := "api repos/devsy-org/devsy/actions/workflows/pr-ci.yml/runs?"
	s.True(strings.HasPrefix(call, prefix), call)
	for _, want := range []string{"event=push", "branch=main", "head_sha=" + scopeTestBefore} {
		s.Contains(call, want)
	}
}

func (s *scopeSuite) TestGitHubRunHistoryNeedsARepository() {
	_, err := ghRunHistory{
		runner: &scriptedRunner{},
	}.Conclusions(
		context.Background(),
		scopeTestBefore,
	)

	s.ErrorIs(err, errNoRepository)
}

func (s *scopeSuite) TestWorkflowSharesOneNonCancellingMainGroup() {
	raw, err := readWorkflow()
	s.Require().NoError(err)

	s.Contains(raw, "cancel-in-progress: ${{ github.event_name == 'pull_request' }}")
	s.Contains(raw, "GH_TOKEN: ${{ github.token }}\n        run: go run ./hack/ci scope")
	s.Contains(
		raw,
		"    permissions:\n      actions: read\n      contents: read\n      pull-requests: read",
	)
	require.NotContains(s.T(), raw, "cancel-in-progress: true")
}
