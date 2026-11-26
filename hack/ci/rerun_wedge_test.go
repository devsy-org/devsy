package main

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	wedgedUpFeatures = `{"name":"Test up-features on windows-latest","conclusion":"cancelled",` +
		`"runner_name":"GitHub Actions 1000312899","labels":["windows-latest"],` +
		`"completed_at":"2026-11-25T23:03:32Z","steps":[` +
		`{"name":"install podman (Windows)","conclusion":"success","started_at":"2026-11-25T22:38:55Z"},` +
		`{"name":"start podman (Windows)","conclusion":null,"started_at":"2026-11-25T22:38:59Z"},` +
		`{"name":"setup WSL2 Ubuntu","conclusion":null,"started_at":null}]}`
	cancelledPush = `{"id":7,"run_attempt":1,"status":"completed","conclusion":"cancelled",` +
		`"event":"push","head_branch":"main","workflow_id":3}`
	windowsUpFeatures = "Test up-features on windows-latest"
	startPodmanStep   = "start podman (Windows)"
)

func stuckJob(step string, ran time.Duration, conclusion string, labels ...string) ghJob {
	end := time.Date(2026, 11, 25, 23, 3, 32, 0, time.UTC)
	return ghJob{
		Name:        windowsUpFeatures,
		Conclusion:  cancelledState,
		Labels:      labels,
		CompletedAt: rawTime(end),
		Steps: []ghStep{
			{Name: step, Conclusion: conclusion, StartedAt: rawTime(end.Add(-ran))},
		},
	}
}

func rawTime(t time.Time) json.RawMessage {
	raw, _ := json.Marshal(t)
	return raw
}

func failedStuckJob(job ghJob) ghJob {
	job.Conclusion = failureState
	return job
}

type wedgeCase struct {
	name  string
	run   ghRun
	job   ghJob
	rerun bool
}

const (
	winLabel   = "windows-latest"
	wslStep    = "setup WSL2 Ubuntu"
	longStuck  = 25 * time.Minute
	pushEvent  = "push"
	pullEvent  = "pull_request"
	retryStep  = "setup WSL2 Ubuntu (retry)"
	linuxLabel = "ubuntu-latest"
)

func pushCase(name string, job ghJob, rerun bool) wedgeCase {
	run := ghRun{
		ID:         7,
		Attempt:    1,
		Status:     statusDone,
		Conclusion: cancelledState,
		Event:      pushEvent,
	}
	return wedgeCase{name: name, run: run, job: job, rerun: rerun}
}

func wedgeCases() []wedgeCase {
	stuck := func(step, conclusion string, ran time.Duration, label string) ghJob {
		return stuckJob(step, ran, conclusion, label)
	}
	neverStarted := stuckJob(startPodmanStep, 0, "", winLabel)
	neverStarted.Steps[0].StartedAt = nil
	replaced := pushCase("pull request run cancelled by a newer push",
		stuck(startPodmanStep, "", longStuck, winLabel), false)
	replaced.run.Event = pullEvent
	return []wedgeCase{
		pushCase(
			"podman start past its timeout",
			stuck(startPodmanStep, "", longStuck, winLabel),
			true,
		),
		pushCase(
			"WSL2 setup past its timeout",
			stuck(wslStep, cancelledState, longStuck, winLabel),
			true,
		),
		pushCase("WSL2 setup retry step", stuck(retryStep, "", longStuck, winLabel), true),
		pushCase(
			"step inside the overrun window",
			stuck(startPodmanStep, "", 6*time.Minute, winLabel),
			false,
		),
		pushCase(
			"step that finished",
			stuck(startPodmanStep, "success", longStuck, winLabel),
			false,
		),
		pushCase(
			"step that failed",
			stuck(startPodmanStep, failureState, longStuck, winLabel),
			false,
		),
		pushCase("step outside the WSL2 setup", stuck("run tests", "", longStuck, winLabel), false),
		pushCase(
			"Linux podman step name",
			stuck("start podman (Linux)", "", longStuck, winLabel),
			false,
		),
		pushCase("step that never started", neverStarted, false),
		pushCase(
			"exactly at the overrun limit",
			stuck(startPodmanStep, "", wedgeStepOverrun, winLabel),
			false,
		),
		pushCase("just past the overrun limit",
			stuck(startPodmanStep, "", wedgeStepOverrun+time.Second, winLabel), true),
		pushCase("job that failed instead of being cancelled",
			failedStuckJob(stuck(startPodmanStep, "", longStuck, winLabel)), false),
		pushCase("Linux job", stuck(startPodmanStep, "", longStuck, linuxLabel), false),
		replaced,
	}
}

func (s *rerunSuite) TestWedgedSetupSelection() {
	for _, tt := range wedgeCases() {
		s.Run(tt.name, func() {
			plan := selectRerun(tt.run, []ghJob{tt.job}, map[string]bool{aggregator: true})

			s.Equal(tt.rerun, plan.rerun, plan.reason)
			if tt.rerun {
				s.Equal("every failed job is "+wedgeCause, plan.reason)
			}
		})
	}
}

func (s *rerunSuite) TestWedgedSetupRerunsFromAPIJobs() {
	fake := &rerunFakeRunner{outputs: map[string]string{
		testRunPath:  cancelledPush,
		testJobsPath: wedgedUpFeatures + "\n" + aggregatorJob + "\n" + passedJob + "\n",
	}}

	out, err := s.runRerun(fake, "--ignore", aggregator)

	s.Require().NoError(err)
	s.Contains(out, "::notice::run 7: rerunning failed jobs: every failed job is "+wedgeCause)
	s.Contains(out, "\""+windowsUpFeatures+"\"")
	s.Equal("api -X POST "+testRunPath+"/rerun-failed-jobs", fake.calls[len(fake.calls)-1])
}

func (s *rerunSuite) TestWedgedSetupWithOtherCausesNamesEachCause() {
	run := ghRun{ID: 7, Attempt: 1, Status: statusDone, Conclusion: cancelledState, Event: "push"}
	jobs := []ghJob{
		stuckJob(startPodmanStep, 25*time.Minute, "", "windows-latest"),
		job(macUnitTests, "cancelled", "", "macos-latest"),
	}

	plan := selectRerun(run, jobs, nil)

	s.True(plan.rerun)
	s.Equal("every failed job is "+noRunnerCause+" or "+wedgeCause, plan.reason)
}

func (s *rerunSuite) TestMalformedTimestampSkipsOnlyTheWedgeCheck() {
	badStep := `{"name":"Lint","conclusion":"success","labels":["ubuntu-latest"],` +
		`"completed_at":"yesterday","steps":[{"name":"x","started_at":12}]}`
	badWedge := strings.Replace(wedgedUpFeatures, "2026-11-25T23:03:32Z", "not-a-time", 1)
	tests := []struct {
		name   string
		jobs   string
		rerun  bool
		reason string
	}{
		{
			name:   "no runner retry still runs",
			jobs:   noRunnerJob + "\n" + badStep + "\n" + aggregatorJob,
			rerun:  true,
			reason: noRunnerReason,
		},
		{
			name:   "wedged job with a bad end time is not retried",
			jobs:   badWedge + "\n" + aggregatorJob,
			reason: "no rerun: " + logSafe(windowsUpFeatures) + " ended \"cancelled\"",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			fake := &rerunFakeRunner{outputs: map[string]string{
				testRunPath: failedRun, testJobsPath: tt.jobs,
			}}

			out, err := s.runRerun(fake, "--ignore", aggregator)

			s.Require().NoError(err)
			s.Contains(out, tt.reason)
			posted := fake.calls[len(fake.calls)-1] == "api -X POST "+testRunPath+"/rerun-failed-jobs"
			s.Equal(tt.rerun, posted)
		})
	}
}

func (s *rerunSuite) TestPullRequestRunWithWedgedJob() {
	failedPR := strings.Replace(failedRun, `"push"`, `"pull_request"`, 1)
	cancelledPR := strings.Replace(failedPR, `"failure"`, `"cancelled"`, 1)
	tests := []struct {
		name   string
		run    string
		latest string
		rerun  bool
	}{
		{name: "failed run that is the latest", run: failedPR, latest: "7\n", rerun: true},
		{name: "failed run that a newer push replaced", run: failedPR, latest: "9\n"},
		{name: "cancelled run", run: cancelledPR, latest: "7\n"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			fake := &rerunFakeRunner{outputs: map[string]string{
				testRunPath:  tt.run,
				testJobsPath: wedgedUpFeatures + "\n" + passedJob,
				testLatest:   tt.latest,
			}}

			_, err := s.runRerun(fake)

			s.Require().NoError(err)
			posted := fake.calls[len(fake.calls)-1] == "api -X POST "+testRunPath+"/rerun-failed-jobs"
			s.Equal(tt.rerun, posted)
		})
	}
}
