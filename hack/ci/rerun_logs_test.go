package main

import (
	"strings"
)

const (
	winJobPath  = "repos/o/r/actions/jobs/11/logs"
	winUnitName = "Unit Tests (windows-latest)"
	winJobJSON  = `{"id":11,"name":"` + winUnitName + `","conclusion":"failure",` +
		`"runner_name":"w1","labels":["windows-latest"]}`
	linuxFailJSON = `{"id":12,"name":"Lint","conclusion":"failure","runner_name":"r2",` +
		`"labels":["ubuntu-latest"]}`
	amxHostLog   = "2026-11-25T10:00:00Z XSTATE features=0x602e7 XTILECFG=True XTILEDATA=True\n"
	plainHostLog = "2026-11-25T10:00:00Z XSTATE features=0x602e7 XTILECFG=False XTILEDATA=False\n"
	runtimeCrash = "2026-11-25T10:05:00Z fatal error: found bad pointer in Go heap\n"
)

func (s *rerunSuite) TestProbe() {
	tests := []struct {
		name   string
		writes []string
		crash  bool
	}{
		{name: "fatal error on AMX host", writes: []string{amxHostLog, runtimeCrash}, crash: true},
		{
			name:   "access violation on AMX host",
			writes: []string{amxHostLog, "Exception 0xc0000005\n"},
			crash:  true,
		},
		{
			name:   "crash before the host line",
			writes: []string{runtimeCrash, amxHostLog},
			crash:  true,
		},
		{name: "fatal error without AMX", writes: []string{plainHostLog, runtimeCrash}},
		{name: "AMX host without a crash", writes: []string{amxHostLog, "FAIL pkg 1.2s\n"}},
		{name: "no host line", writes: []string{runtimeCrash}},
		{
			name:   "ordinary git failure",
			writes: []string{amxHostLog, "fatal: not a git repository\n"},
		},
		{
			name:   "markers split across writes",
			writes: []string{"XTILEDATA=Tr", "ue\nfatal err", "or: bad\n"},
			crash:  true,
		},
		{
			name:   "markers split one byte at a time",
			writes: strings.Split(amxHostLog+runtimeCrash, ""),
			crash:  true,
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			var probe logProbe
			for _, w := range tt.writes {
				n, err := probe.Write([]byte(w))
				s.Require().NoError(err)
				s.Equal(len(w), n)
			}

			s.Equal(tt.crash, probe.crashOnAMX())
		})
	}
}

func (s *rerunSuite) TestSelectionWithAMXCrashes() {
	run := ghRun{ID: 7, Attempt: 1, Status: statusDone, Conclusion: failureState}
	crash := job(winUnitName, "failure", "w1", "windows-latest")
	crash.amxCrash = true
	noRunner := job(macUnitTests, "cancelled", "", "macos-latest")
	realFailure := job("Windows Build", "failure", "w2", "windows-latest")
	tests := []struct {
		name   string
		jobs   []ghJob
		rerun  bool
		reason string
		names  []string
	}{
		{
			name: "only an AMX crash", jobs: []ghJob{crash}, rerun: true,
			reason: amxCrashReason, names: []string{winUnitName},
		},
		{
			name: "AMX crash and macOS no-runner", jobs: []ghJob{crash, noRunner}, rerun: true,
			reason: mixedReason, names: []string{winUnitName, macUnitTests},
		},
		{
			name: "AMX crash and a real Windows failure", jobs: []ghJob{crash, realFailure},
			reason: "\"Windows Build\" ended \"failure\"",
		},
		{
			name: "AMX crash and a Linux failure",
			jobs: []ghJob{
				crash,
				job("Lint", "failure", "r2", "ubuntu-latest"),
			},
			reason: "\"Lint\" ended",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			plan := selectRerun(run, tt.jobs, map[string]bool{aggregator: true})

			s.Equal(tt.rerun, plan.rerun)
			s.Contains(plan.reason, tt.reason)
			s.Equal(tt.names, plan.jobs)
		})
	}
}

func (s *rerunSuite) TestRerunsWindowsJobThatCrashedOnAMXHost() {
	fake := &rerunFakeRunner{outputs: map[string]string{
		testRunPath:  failedRun,
		testJobsPath: winJobJSON + "\n" + aggregatorJob + "\n" + passedJob + "\n",
		winJobPath:   amxHostLog + runtimeCrash,
	}}

	out, err := s.runRerun(fake, "--ignore", aggregator)

	s.Require().NoError(err)
	s.Contains(
		out,
		"::notice::run 7: rerunning failed jobs: "+amxCrashReason+"\n  \""+winUnitName+"\"\n",
	)
	s.Contains(fake.calls, "api --allow-escape-sequences "+winJobPath)
	s.Equal("api -X POST "+testRunPath+"/rerun-failed-jobs", fake.calls[len(fake.calls)-1])
}

func (s *rerunSuite) TestNoRerunForWindowsFailureOffAMX() {
	for name, log := range map[string]string{
		"no AMX":   plainHostLog + runtimeCrash,
		"no crash": amxHostLog + "--- FAIL: TestX\n",
	} {
		s.Run(name, func() {
			fake := &rerunFakeRunner{outputs: map[string]string{
				testRunPath: failedRun, testJobsPath: winJobJSON, winJobPath: log,
			}}

			out, err := s.runRerun(fake)

			s.Require().NoError(err)
			s.Contains(out, "no rerun: \""+winUnitName+"\" ended \"failure\"")
			s.NotContains(strings.Join(fake.calls, "\n"), "rerun-failed-jobs")
		})
	}
}

func (s *rerunSuite) TestLogsAreOnlyReadForRetryableWindowsFailures() {
	tests := []struct {
		name  string
		run   string
		jobs  string
		args  []string
		reads bool
	}{
		{name: "Linux failure", run: failedRun, jobs: linuxFailJSON},
		{
			name: "ignored Windows job",
			run:  failedRun,
			jobs: winJobJSON,
			args: []string{"--ignore", winUnitName},
		},
		{
			name: "second attempt",
			run:  strings.Replace(failedRun, `"run_attempt":1`, `"run_attempt":2`, 1),
			jobs: winJobJSON,
		},
		{name: "first attempt", run: failedRun, jobs: winJobJSON, reads: true},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			fake := &rerunFakeRunner{outputs: map[string]string{
				testRunPath: tt.run, testJobsPath: tt.jobs, winJobPath: amxHostLog + runtimeCrash,
			}}

			_, err := s.runRerun(fake, tt.args...)

			s.Require().NoError(err)
			s.Equal(tt.reads, strings.Contains(strings.Join(fake.calls, "\n"), "jobs/11/logs"))
		})
	}
}

func (s *rerunSuite) TestLogErrorFails() {
	fake := &rerunFakeRunner{outputs: map[string]string{
		testRunPath: failedRun, testJobsPath: winJobJSON,
	}}

	_, err := s.runRerun(fake)

	s.Require().ErrorContains(err, "unexpected call: api --allow-escape-sequences "+winJobPath)
}
