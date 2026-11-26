package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

const (
	goFatalMarker   = "fatal error: "
	accessViolation = "0xc0000005"
	amxOnMarker     = "XTILEDATA=True"
	// markerOverlap keeps enough of the previous write to match a marker
	// split across two writes.
	markerOverlap = 16
)

// logProbe scans a job log as it streams, without holding the whole log, for
// a Go runtime crash on a host that reported AMX state on (golang/go#81238).
// The log is written by the pull request's own code, so a match only allows
// the single retry the run already gets.
type logProbe struct {
	tail  []byte
	fatal bool
	amx   bool
}

func (p *logProbe) Write(b []byte) (int, error) {
	window := slices.Concat(p.tail, b)
	text := string(window)
	p.fatal = p.fatal || strings.Contains(text, goFatalMarker) ||
		strings.Contains(text, accessViolation)
	p.amx = p.amx || strings.Contains(text, amxOnMarker)
	p.tail = slices.Clone(window[max(0, len(window)-markerOverlap):])
	return len(b), nil
}

func (p *logProbe) crashOnAMX() bool {
	return p.fatal && p.amx
}

func (job ghJob) windowsFailure() bool {
	return job.Conclusion == "failure" && job.isWindows()
}

// markAMXCrashes reads the log of each failed Windows job and flags the ones
// that crashed in the Go runtime on an AMX host. A log that cannot be read
// (expired, missing, a server error) only means that job is not flagged, so
// it never stops the rest of the decision.
func (c rerunCommand) markAMXCrashes(ctx context.Context, opts rerunOptions, jobs []ghJob) {
	for i := range jobs {
		if opts.ignore[jobs[i].Name] || !jobs[i].windowsFailure() {
			continue
		}
		crash, err := c.crashedOnAMX(ctx, opts, jobs[i])
		if err != nil {
			_, _ = fmt.Fprintf(
				c.stdout,
				"::warning::run %d: cannot read the log of %s, so it is not an AMX crash: %v\n",
				opts.runID, logSafe(jobs[i].Name), err,
			)
		}
		jobs[i].amxCrash = crash
	}
}

func (c rerunCommand) crashedOnAMX(
	ctx context.Context,
	opts rerunOptions,
	job ghJob,
) (bool, error) {
	var probe logProbe
	path := fmt.Sprintf("repos/%s/actions/jobs/%d/logs", opts.repo, job.ID)
	// Job logs carry terminal escape sequences, which gh refuses to write
	// unless told to; the probe is the only reader.
	if err := c.gh(ctx, &probe, "--allow-escape-sequences", path); err != nil {
		return false, err
	}
	return probe.crashOnAMX(), nil
}
