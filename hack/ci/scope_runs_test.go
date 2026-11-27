package main

import (
	"bytes"
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
		{"still running", fakeRuns{conclusions: []string{noConclusion}}, false},
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
	runner := &scriptedRunner{outputs: map[string]string{apiCommand: "cancelled\nnone\nsuccess\n"}}
	history := ghRunHistory{runner: runner, repo: "devsy-org/devsy"}

	conclusions, err := history.Conclusions(context.Background(), scopeTestBefore)

	s.Require().NoError(err)
	s.Equal([]string{cancelledState, noConclusion, resultSuccess}, conclusions)
	call := runner.calls[0]
	prefix := "api repos/devsy-org/devsy/actions/workflows/pr-ci.yml/runs?"
	s.True(strings.HasPrefix(call, prefix), call)
	for _, want := range []string{"branch=main", "head_sha=" + scopeTestBefore} {
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

func (s *scopeSuite) TestWorkflowEnsuresMainsTipHasARun() {
	raw, err := readWorkflow()
	s.Require().NoError(err)

	s.Contains(raw, "  ensure-tip:\n    name: Main Tip Run\n    if: github.event_name == 'push'")
	s.Contains(raw, "    permissions:\n      actions: write\n      contents: read\n")
	s.Contains(raw, "GH_TOKEN: ${{ github.token }}\n          HEAD_SHA: ${{ github.sha }}\n")
	s.Contains(raw, "run: go run ./hack/ci ensure-tip --sha \"$HEAD_SHA\"")
	s.Contains(
		raw,
		"replaced it\n        continue-on-error: true\n        env:\n          GH_TOKEN:",
	)
	s.Equal(1, strings.Count(raw, "actions: write"))
}

// The scope command must consult the previous run, or a replaced run's commit
// could be skipped as documentation only.
func (s *scopeSuite) TestScopeCommandRequiresTheVerdictOnThePreviousRun() {
	tests := []struct {
		name       string
		conclusion string
		light      string
	}{
		{"previous run passed", resultSuccess, "light=true\n"},
		{"previous run replaced", cancelledState, "light=false\n"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			runner := routeRunner{
				"cat-file": "tree abc\nparent " + scopeTestBefore + "\n",
				"diff":     readmeFile + "\x00",
				apiCommand: tt.conclusion + "\n",
			}
			var stdout, stderr bytes.Buffer

			code := scopeWith(
				runner,
				[]string{"--before", scopeTestBefore, shaFlag, scopeTestSHA, repoFlag, "o/r"},
				&stdout,
				&stderr,
			)

			s.Equal(0, code)
			s.Contains(stdout.String(), tt.light)
		})
	}
}
