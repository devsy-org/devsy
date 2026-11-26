package main

import (
	"encoding/json"
	"slices"
	"strings"
	"time"
)

// wedgeStepOverrun is how long a WSL2 setup step may run before the host is
// presumed stuck. Every step in wedgeSetupSteps has a 5 minute step timeout,
// and the podman one also has an in-process deadline of 280 seconds.
const wedgeStepOverrun = 10 * time.Minute

// wedgeSetupSteps are the Windows steps that bring up WSL2. When the host
// hangs there, no timer inside the job fires and no log is uploaded, so the
// job only ends when GitHub cancels it at the job time limit.
var wedgeSetupSteps = []string{
	"start podman (Windows)",
	"setup WSL2 Ubuntu",
	"setup WSL2 Ubuntu (retry)",
}

// apiTime reads an API timestamp and returns the zero time for a null or
// malformed one, so a bad value disables only the check that needs it instead
// of failing the decode of the whole job list.
func apiTime(raw json.RawMessage) time.Time {
	var parsed time.Time
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return time.Time{}
	}
	return parsed
}

// ghStep holds the step fields the wedge check reads.
type ghStep struct {
	Name       string          `json:"name"`
	Conclusion string          `json:"conclusion"`
	StartedAt  json.RawMessage `json:"started_at"`
}

func (job ghJob) isWindows() bool {
	return slices.ContainsFunc(job.Labels, func(label string) bool {
		return strings.HasPrefix(label, "windows")
	})
}

// wedgedSetup reports a Windows job cancelled while one of its WSL2 setup
// steps was still running long past that step's own timeout.
func (job ghJob) wedgedSetup() bool {
	end := apiTime(job.CompletedAt)
	if job.Conclusion != cancelledState || end.IsZero() || !job.isWindows() {
		return false
	}
	return slices.ContainsFunc(job.Steps, func(step ghStep) bool {
		return step.stuckAt(end)
	})
}

func (step ghStep) stuckAt(end time.Time) bool {
	started := apiTime(step.StartedAt)
	unfinished := step.Conclusion == "" || step.Conclusion == cancelledState
	return unfinished && !started.IsZero() &&
		slices.Contains(wedgeSetupSteps, step.Name) &&
		end.Sub(started) > wedgeStepOverrun
}
