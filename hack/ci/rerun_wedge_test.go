package main

import (
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
		CompletedAt: end,
		Steps:       []ghStep{{Name: step, Conclusion: conclusion, StartedAt: end.Add(-ran)}},
	}
}

func (s *rerunSuite) TestWedgedSetupSelection() {
	push := ghRun{ID: 7, Attempt: 1, Status: statusDone, Conclusion: cancelledState, Event: "push"}
	replaced := push
	replaced.Event = "pull_request"
	const (
		win   = "windows-latest"
		linux = "ubuntu-latest"
		wsl   = "setup WSL2 Ubuntu"
		long  = 25 * time.Minute
	)
	neverStarted := stuckJob(startPodmanStep, 0, "", win)
	neverStarted.Steps[0].StartedAt = time.Time{}
	tests := []struct {
		name  string
		run   ghRun
		job   ghJob
		rerun bool
	}{
		{
			name: "podman start past its timeout", run: push, rerun: true,
			job: stuckJob(startPodmanStep, long, "", win),
		},
		{
			name: "WSL2 setup past its timeout", run: push, rerun: true,
			job: stuckJob(wsl, long, cancelledState, win),
		},
		{
			name: "step inside the overrun window", run: push,
			job: stuckJob(startPodmanStep, 6*time.Minute, "", win),
		},
		{
			name: "step that finished", run: push,
			job: stuckJob(startPodmanStep, long, "success", win),
		},
		{
			name: "step outside the WSL2 setup", run: push,
			job: stuckJob("run tests", long, "", win),
		},
		{name: "step that never started", run: push, job: neverStarted},
		{
			name: "Linux job", run: push,
			job: stuckJob(startPodmanStep, long, "", linux),
		},
		{
			name: "pull request run replaced by a newer push", run: replaced,
			job: stuckJob(startPodmanStep, long, "", win),
		},
	}
	for _, tt := range tests {
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
