package main

import (
	"bytes"
	"context"
)

const (
	repoFlag     = "--repo"
	shaFlag      = "--sha"
	noConclusion = "none"
	apiCommand   = "api"
)

// routeRunner answers a command with the output stored under its first argument.
type routeRunner map[string]string

func (r routeRunner) Run(_ context.Context, spec commandSpec) error {
	if out, ok := r[spec.Args[0]]; ok && spec.Stdout != nil {
		_, _ = spec.Stdout.Write([]byte(out))
	}
	return nil
}

type fakeTip struct {
	tip         string
	conclusions []string
	tipErr      error
	runsErr     error
	dispatchErr error
	dispatched  int
}

func (f *fakeTip) Tip(context.Context) (string, error) { return f.tip, f.tipErr }

func (f *fakeTip) Conclusions(context.Context, string) ([]string, error) {
	return f.conclusions, f.runsErr
}

func (f *fakeTip) Dispatch(context.Context) error {
	f.dispatched++
	return f.dispatchErr
}

func (s *scopeSuite) TestTipRunIsStartedOnlyWhenMissing() {
	tests := []struct {
		name       string
		client     *fakeTip
		dispatched int
	}{
		{"run is for the tip", &fakeTip{tip: scopeTestSHA}, 0},
		{
			"tip replaced in the queue",
			&fakeTip{tip: scopeTestBefore, conclusions: []string{cancelledState}},
			1,
		},
		{"tip has no run", &fakeTip{tip: scopeTestBefore}, 1},
		{
			"tip run is waiting",
			&fakeTip{tip: scopeTestBefore, conclusions: []string{noConclusion}},
			0,
		},
		{
			"tip run passed",
			&fakeTip{tip: scopeTestBefore, conclusions: []string{cancelledState, resultSuccess}},
			0,
		},
		{"tip run failed", &fakeTip{tip: scopeTestBefore, conclusions: []string{resultFailure}}, 0},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			message, err := ensureTipRun(context.Background(), tt.client, scopeTestSHA)

			s.Require().NoError(err)
			s.NotEmpty(message)
			s.Equal(tt.dispatched, tt.client.dispatched)
		})
	}
}

func (s *scopeSuite) TestTipRunReportsLookupAndDispatchErrors() {
	tests := []struct {
		name   string
		client *fakeTip
	}{
		{"cannot read the tip", &fakeTip{tipErr: errScopeTest}},
		{"cannot read the tip's runs", &fakeTip{tip: scopeTestBefore, runsErr: errScopeTest}},
		{"cannot dispatch", &fakeTip{tip: scopeTestBefore, dispatchErr: errScopeTest}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			_, err := ensureTipRun(context.Background(), tt.client, scopeTestSHA)

			s.ErrorIs(err, errScopeTest)
		})
	}
}

func (s *scopeSuite) TestGitHubTipAndDispatch() {
	runner := &scriptedRunner{outputs: map[string]string{apiCommand: scopeTestBefore + "\n"}}
	history := ghRunHistory{runner: runner, repo: "devsy-org/devsy"}

	tip, err := history.Tip(context.Background())
	s.Require().NoError(err)
	s.Equal(scopeTestBefore, tip)
	s.Require().NoError(history.Dispatch(context.Background()))

	s.Equal("api repos/devsy-org/devsy/git/ref/heads/main --jq .object.sha", runner.calls[0])
	s.Equal(
		"api --method POST repos/devsy-org/devsy/actions/workflows/pr-ci.yml/dispatches -f ref=main",
		runner.calls[1],
	)
	_, err = ghRunHistory{runner: runner}.Tip(context.Background())
	s.ErrorIs(err, errNoRepository)
	s.ErrorIs(ghRunHistory{runner: runner}.Dispatch(context.Background()), errNoRepository)
}

func (s *scopeSuite) TestEnsureTipCommand() {
	var stdout, stderr bytes.Buffer
	args := []string{shaFlag, scopeTestSHA, repoFlag, "o/r"}

	code := ensureTipWith(routeRunner{apiCommand: scopeTestSHA + "\n"}, args, &stdout, &stderr)

	s.Equal(0, code)
	s.Contains(stdout.String(), "tip")

	stdout.Reset()
	code = ensureTipWith(routeRunner{apiCommand: scopeTestBefore + "\n"}, args, &stdout, &stderr)
	s.Equal(0, code)
	s.Contains(stdout.String(), "already has a run")

	s.Equal(2, ensureTipWith(routeRunner{}, nil, &stdout, &stderr))
	s.Equal(2, ensureTipWith(routeRunner{}, []string{"--nope"}, &stdout, &stderr))
	s.Equal(
		1,
		ensureTipWith(
			routeRunner{},
			[]string{shaFlag, scopeTestSHA, repoFlag, ""},
			&stdout,
			&stderr,
		),
	)
}
