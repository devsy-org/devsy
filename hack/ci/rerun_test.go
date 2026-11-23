package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

const (
	testRunPath  = "repos/o/r/actions/runs/7"
	testJobsPath = testRunPath + "/attempts/1/jobs?per_page=100"
	testLatest   = "repos/o/r/actions/workflows/3/runs"
	failedRun    = `{"id":7,"run_attempt":1,"status":"completed","conclusion":"failure",` +
		`"event":"push","head_branch":"main","workflow_id":3}`
	noRunnerJob = `{"name":"Unit Tests (macos-latest)","conclusion":"cancelled",` +
		`"runner_name":"","labels":["macos-latest"]}`
	aggregatorJob = `{"name":"CI Success","conclusion":"failure","runner_name":"r1","labels":["ubuntu-latest"]}`
	testRepo      = "o/r"
	runIDFlag     = "--run-id"
	statusDone    = "completed"
	aggregator    = "CI Success"
	macUnitTests  = "Unit Tests (macos-latest)"
	passedJob     = `{"name":"Lint","conclusion":"success","runner_name":"r2","labels":["ubuntu-latest"]}`
)

// rerunFakeRunner serves `gh api` output by API path and records every call.
type rerunFakeRunner struct {
	outputs map[string]string
	calls   []string
}

func (f *rerunFakeRunner) Run(_ context.Context, spec commandSpec) error {
	call := strings.Join(spec.Args, " ")
	f.calls = append(f.calls, call)
	for path, out := range f.outputs {
		if slices.Contains(spec.Args, path) {
			_, err := io.WriteString(spec.Stdout, out)
			return err
		}
	}
	if slices.Contains(spec.Args, "POST") {
		return nil
	}
	return errors.New("unexpected call: " + call)
}

type rerunSuite struct {
	suite.Suite
}

func TestRerunSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(rerunSuite))
}

func job(name, conclusion, runner string, labels ...string) ghJob {
	return ghJob{Name: name, Conclusion: conclusion, RunnerName: runner, Labels: labels}
}

type selectionCase struct {
	name   string
	run    ghRun
	jobs   []ghJob
	rerun  bool
	reason string
}

func selectionCases() []selectionCase {
	run := ghRun{ID: 7, Attempt: 1, Status: statusDone, Conclusion: "failure"}
	noRunner := job(macUnitTests, "cancelled", "", "macos-latest")
	return []selectionCase{
		{
			name:   "only macOS no-runner cancels",
			run:    run,
			rerun:  true,
			reason: noRunnerReason,
			jobs: []ghJob{
				noRunner,
				job("Build CLI Binary on macos-latest", "cancelled", "", "macos-15"),
				job(aggregator, "failure", "r1", "ubuntu-latest"),
				job("Lint", "success", "r2", "ubuntu-latest"),
				job("e2e", "skipped", ""),
			},
		},
		{
			name:   "second attempt",
			run:    ghRun{Attempt: 2, Status: statusDone, Conclusion: "failure"},
			jobs:   []ghJob{noRunner},
			reason: "attempt 2 is not the first attempt",
		},
		{
			name:   "cancelled run",
			run:    ghRun{Attempt: 1, Status: statusDone, Conclusion: "cancelled"},
			jobs:   []ghJob{noRunner},
			reason: "not completed/failure",
		},
		{
			name: "run in progress", run: ghRun{Attempt: 1, Status: "in_progress"},
			jobs: []ghJob{noRunner}, reason: "not completed/failure",
		},
		{
			name: "macOS job that ran", run: run, reason: "ended cancelled on runner \"m1\"",
			jobs: []ghJob{job(macUnitTests, "cancelled", "m1", "macos-latest")},
		},
		{
			name: "macOS test failure", run: run, reason: "ended failure",
			jobs: []ghJob{job(macUnitTests, "failure", "m1", "macos-latest")},
		},
		{
			name: "Linux no-runner cancel", run: run, reason: "Build ended cancelled",
			jobs: []ghJob{noRunner, job("Build", "cancelled", "", "ubuntu-latest")},
		},
		{
			name: "unlabelled cancel", run: run, reason: "matrix.title ended cancelled",
			jobs: []ghJob{noRunner, job("matrix.title", "cancelled", "")},
		},
		{
			name: "timed out macOS job", run: run, reason: "ended timed_out",
			jobs: []ghJob{job(macUnitTests, "timed_out", "", "macos-latest")},
		},
		{
			name: "only the ignored job failed", run: run, reason: "no failed jobs",
			jobs: []ghJob{job(aggregator, "failure", "r1", "ubuntu-latest")},
		},
	}
}

func (s *rerunSuite) TestSelection() {
	ignore := map[string]bool{aggregator: true}
	for _, tt := range selectionCases() {
		s.Run(tt.name, func() {
			plan := selectNoRunnerRerun(tt.run, tt.jobs, ignore)

			s.Equal(tt.rerun, plan.rerun)
			s.Contains(plan.reason, tt.reason)
			if tt.rerun {
				s.Equal([]string{macUnitTests, "Build CLI Binary on macos-latest"}, plan.jobs)
			}
		})
	}
}

func (s *rerunSuite) TestRerunsAndLogsJobs() {
	fake := &rerunFakeRunner{outputs: map[string]string{
		testRunPath:  failedRun,
		testJobsPath: noRunnerJob + "\n" + aggregatorJob + "\n" + passedJob + "\n",
	}}

	out, err := s.runRerun(fake, "--ignore", aggregator)

	s.Require().NoError(err)
	s.Contains(
		out,
		"::notice::run 7: rerunning failed jobs: "+noRunnerReason+"\n  "+macUnitTests+"\n",
	)
	s.Equal("api -X POST "+testRunPath+"/rerun-failed-jobs", fake.calls[len(fake.calls)-1])
}

func (s *rerunSuite) TestDryRunDoesNotRerun() {
	fake := &rerunFakeRunner{
		outputs: map[string]string{testRunPath: failedRun, testJobsPath: noRunnerJob},
	}

	out, err := s.runRerun(fake, "--dry-run")

	s.Require().NoError(err)
	s.Contains(out, "rerunning failed jobs")
	s.Len(fake.calls, 2)
}

func (s *rerunSuite) TestNoRerunWhenAnotherJobFailed() {
	fake := &rerunFakeRunner{outputs: map[string]string{
		testRunPath:  failedRun,
		testJobsPath: noRunnerJob + aggregatorJob,
	}}

	out, err := s.runRerun(fake)

	s.Require().NoError(err)
	s.Contains(out, "run 7: no rerun: CI Success ended failure")
	s.Len(fake.calls, 2)
}

func (s *rerunSuite) TestPullRequestRunNeedsToBeLatest() {
	prRun := strings.Replace(failedRun, `"push"`, `"pull_request"`, 1)
	tests := []struct {
		name   string
		latest string
		rerun  bool
	}{
		{name: "latest run", latest: "7\n", rerun: true},
		{name: "superseded run", latest: "9\n"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			fake := &rerunFakeRunner{outputs: map[string]string{
				testRunPath: prRun, testJobsPath: noRunnerJob, testLatest: tt.latest,
			}}

			out, err := s.runRerun(fake)

			s.Require().NoError(err)
			if tt.rerun {
				s.Len(fake.calls, 4)
				return
			}
			s.Contains(out, "no rerun: a newer run 9 exists for branch main")
			s.Len(fake.calls, 3)
		})
	}
}

func (s *rerunSuite) TestAPIErrorFails() {
	fake := &rerunFakeRunner{outputs: map[string]string{testRunPath: "{not json"}}

	_, err := s.runRerun(fake)

	s.Require().ErrorContains(err, "decode "+testRunPath)
}

func (s *rerunSuite) TestArgs() {
	getenv := func(string) string { return testRepo }
	var output bytes.Buffer

	opts, err := parseRerunArgs([]string{runIDFlag, "7", "--ignore", aggregator}, &output, getenv)

	s.Require().NoError(err)
	s.Equal(rerunOptions{repo: testRepo, runID: 7, ignore: map[string]bool{aggregator: true}}, opts)
	for _, args := range [][]string{{}, {runIDFlag, "0"}, {runIDFlag, "7", "stray"}} {
		_, err := parseRerunArgs(args, &output, getenv)
		s.Require().Error(err)
	}
	_, err = parseRerunArgs([]string{runIDFlag, "7"}, &output, func(string) string { return "" })
	s.Require().Error(err)
}

func (s *rerunSuite) TestRetriesDoNotShareThePullRequestGroup() {
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "pr-ci.yml"))
	s.Require().NoError(err)

	s.Contains(
		string(workflow),
		"group: ${{ github.workflow }}-${{ github.event_name == 'pull_request' && "+
			"github.run_attempt == 1 && format('pr-{0}', github.head_ref) || "+
			"format('run-{0}', github.run_id) }}\n",
	)
}

func (s *rerunSuite) runRerun(fake *rerunFakeRunner, args ...string) (string, error) {
	base := []string{"--repo", testRepo, runIDFlag, "7"}
	opts, err := parseRerunArgs(
		append(base, args...),
		io.Discard,
		func(string) string { return "" },
	)
	s.Require().NoError(err)
	var stdout bytes.Buffer
	err = rerunCommand{runner: fake, stdout: &stdout}.run(context.Background(), opts)
	return stdout.String(), err
}
