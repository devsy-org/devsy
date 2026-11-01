package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
)

const (
	defaultNeedsEnv = "NEEDS_JSON"
	changesJob      = "changes"
	goOutput        = "go"
	resultSuccess   = "success"
	resultSkipped   = "skipped"
)

var (
	errNeedsEmpty     = errors.New("needs variable is empty")
	errNeedsInvalid   = errors.New("needs variable is not valid JSON")
	errNeedsNoJobs    = errors.New("needs variable has no jobs")
	errBadGoOutput    = errors.New("changes job go output is not true or false")
	errUnexpectedArgs = errors.New("unexpected arguments")
)

// neededJob is one entry of the toJSON(needs) object.
type neededJob struct {
	Result  string            `json:"result"`
	Outputs map[string]string `json:"outputs"`
}

// successOptions holds the parsed flags of `ci success`.
type successOptions struct {
	needsEnv  string
	alwaysRun map[string]bool
}

// successCheck folds the results of the needed jobs into one required status.
type successCheck struct {
	getenv func(string) string
	stdout io.Writer
	stderr io.Writer
}

func successCLI(args []string, stdout, stderr io.Writer) int {
	check := successCheck{getenv: os.Getenv, stdout: stdout, stderr: stderr}
	return check.run(args)
}

func (c successCheck) run(args []string) int {
	opts, err := parseSuccessArgs(args, c.stderr)
	if err != nil {
		return 2
	}
	needs, err := c.loadNeeds(opts.needsEnv)
	if err != nil {
		_, _ = fmt.Fprintf(c.stdout, "::error::%v\n", err)
		return 1
	}
	return c.report(needs, opts)
}

func parseSuccessArgs(args []string, output io.Writer) (successOptions, error) {
	fs := flag.NewFlagSet("success", flag.ContinueOnError)
	fs.SetOutput(output)
	needsEnv := fs.String(
		"needs-env",
		defaultNeedsEnv,
		"environment variable holding toJSON(needs)",
	)
	alwaysRun := fs.String("always-run", "", "comma-separated jobs that must never be skipped")
	if err := fs.Parse(args); err != nil {
		return successOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(output, "%v: %s\n", errUnexpectedArgs, strings.Join(fs.Args(), " "))
		return successOptions{}, errUnexpectedArgs
	}
	return successOptions{needsEnv: *needsEnv, alwaysRun: splitJobList(*alwaysRun)}, nil
}

// splitJobList turns "a, b,,c" into the set {a, b, c}.
func splitJobList(list string) map[string]bool {
	jobs := make(map[string]bool)
	for job := range strings.SplitSeq(list, ",") {
		if job = strings.TrimSpace(job); job != "" {
			jobs[job] = true
		}
	}
	return jobs
}

// loadNeeds reads and decodes the toJSON(needs) object held in the named variable.
func (c successCheck) loadNeeds(name string) (map[string]neededJob, error) {
	raw := c.getenv(name)
	if raw == "" {
		return nil, fmt.Errorf("%w: %s", errNeedsEmpty, name)
	}
	var needs map[string]neededJob
	if err := json.Unmarshal([]byte(raw), &needs); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", errNeedsInvalid, name, err)
	}
	if len(needs) == 0 {
		return nil, fmt.Errorf("%w: %s", errNeedsNoJobs, name)
	}
	return needs, nil
}

// report prints one ::error:: line per bad job, in sorted job-id order, and returns the exit code.
func (c successCheck) report(needs map[string]neededJob, opts successOptions) int {
	failures := 0
	// Without a changes job nothing proves a skip was legitimate, so skips are rejected.
	goChanged := true
	if changes, ok := needs[changesJob]; ok {
		changed, err := goChangedFrom(changes)
		if err != nil {
			_, _ = fmt.Fprintf(c.stdout, "::error::%v\n", err)
			failures++
		} else {
			goChanged = changed
		}
	} else {
		_, _ = fmt.Fprintf(c.stdout, "::error::%s job missing from %s\n", changesJob, opts.needsEnv)
		failures++
	}

	jobs := slices.Sorted(maps.Keys(needs))
	for _, job := range jobs {
		if problem := jobProblem(job, needs[job], opts.alwaysRun[job], goChanged); problem != "" {
			_, _ = fmt.Fprintf(c.stdout, "::error::%s\n", problem)
			failures++
		}
	}

	if failures > 0 {
		return 1
	}
	_, _ = fmt.Fprintf(
		c.stdout,
		"all %d required jobs passed or were legitimately skipped\n",
		len(jobs),
	)
	return 0
}

// jobProblem returns the error message for a needed job, or "" when the job is acceptable.
func jobProblem(job string, res neededJob, alwaysRun, goChanged bool) string {
	switch res.Result {
	case resultSuccess:
		return ""
	case resultSkipped:
		if alwaysRun || goChanged {
			return job + " ended with result=skipped unexpectedly"
		}
		return ""
	default:
		return job + " ended with result=" + res.Result
	}
}

// goChangedFrom reads the go output of the changes job, trusted only after it succeeded.
func goChangedFrom(changes neededJob) (bool, error) {
	if changes.Result != resultSuccess {
		return false, nil
	}
	switch value := changes.Outputs[goOutput]; value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%w: %q", errBadGoOutput, value)
	}
}
