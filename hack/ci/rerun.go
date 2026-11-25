package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
)

const (
	noRunnerReason = "every failed job is a macOS job cancelled before a runner picked it up"
	amxCrashReason = "every failed job is a Windows job that hit a Go runtime fatal error on a host " +
		"with AMX state on"
	mixedReason = "every failed job is a macOS job cancelled before a runner picked it up " +
		"or a Windows job that hit a Go runtime fatal error on a host with AMX state on"
)

// ghRun holds the workflow run fields the rerun decision reads.
type ghRun struct {
	ID         int64  `json:"id"`
	Attempt    int    `json:"run_attempt"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Event      string `json:"event"`
	HeadBranch string `json:"head_branch"`
	WorkflowID int64  `json:"workflow_id"`
}

// ghJob holds the job fields the rerun decision reads.
type ghJob struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Conclusion string   `json:"conclusion"`
	RunnerName string   `json:"runner_name"`
	Labels     []string `json:"labels"`

	// amxCrash is set from the job log, not from the API.
	amxCrash bool
}

type rerunPlan struct {
	rerun  bool
	reason string
	jobs   []string
}

type rerunOptions struct {
	repo   string
	runID  int64
	ignore map[string]bool
	dryRun bool
}

// rerunCommand reruns a run's failed jobs once when every failure is a macOS job
// that never got a runner or a Windows job that hit golang/go#81238.
type rerunCommand struct {
	runner commandRunner
	stdout io.Writer
}

func rerunCLI(args []string, stdout, stderr io.Writer) int {
	opts, err := parseRerunArgs(args, stderr, os.Getenv)
	if err != nil {
		return 2
	}
	cmd := rerunCommand{runner: execRunner{}, stdout: stdout}
	if err := cmd.run(context.Background(), opts); err != nil {
		_, _ = fmt.Fprintf(stdout, "::error::%v\n", err)
		return 1
	}
	return 0
}

func parseRerunArgs(
	args []string,
	output io.Writer,
	getenv func(string) string,
) (rerunOptions, error) {
	fs := flag.NewFlagSet("rerun-no-runner", flag.ContinueOnError)
	fs.SetOutput(output)
	repo := fs.String("repo", getenv("GITHUB_REPOSITORY"), "owner/name of the repository")
	runID := fs.Int64("run-id", 0, "workflow run to inspect")
	ignore := fs.String("ignore", "", "comma-separated jobs whose result follows the others")
	dryRun := fs.Bool("dry-run", false, "print the decision without rerunning")
	if err := fs.Parse(args); err != nil {
		return rerunOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	if fs.NArg() > 0 || *repo == "" || *runID <= 0 {
		_, _ = fmt.Fprintln(
			output,
			"usage: ci rerun-no-runner --run-id ID [--repo R] [--ignore JOBS]",
		)
		return rerunOptions{}, errUnexpectedArgs
	}
	return rerunOptions{
		repo:   *repo,
		runID:  *runID,
		ignore: splitJobList(*ignore),
		dryRun: *dryRun,
	}, nil
}

func (c rerunCommand) run(ctx context.Context, opts rerunOptions) error {
	var run ghRun
	if err := c.getJSON(ctx, &run, c.runPath(opts)); err != nil {
		return err
	}
	plan, err := c.plan(ctx, opts, run)
	if err != nil {
		return err
	}
	c.report(run, plan)
	if !plan.rerun || opts.dryRun {
		return nil
	}
	return c.gh(ctx, nil, "-X", "POST", c.runPath(opts)+"/rerun-failed-jobs")
}

func (c rerunCommand) plan(ctx context.Context, opts rerunOptions, run ghRun) (rerunPlan, error) {
	jobs, err := c.firstAttemptJobs(ctx, opts)
	if err != nil {
		return rerunPlan{}, err
	}
	if runBlocker(run) == "" {
		if err := c.markAMXCrashes(ctx, opts, jobs); err != nil {
			return rerunPlan{}, err
		}
	}
	plan := selectRerun(run, jobs, opts.ignore)
	if plan.rerun && run.Event == "pull_request" {
		return c.skipSuperseded(ctx, opts, run, plan)
	}
	return plan, nil
}

// runBlocker explains why a run is not a first-attempt failure, or returns "".
func runBlocker(run ghRun) string {
	if run.Attempt != 1 {
		return fmt.Sprintf("attempt %d is not the first attempt", run.Attempt)
	}
	if run.Status != "completed" || run.Conclusion != "failure" {
		return fmt.Sprintf("run is %s/%s, not completed/failure", run.Status, run.Conclusion)
	}
	return ""
}

// selectRerun decides whether the failed jobs of a run's first attempt failed
// only because GitHub never assigned a macOS runner or a Windows job crashed
// in the Go runtime on an AMX host.
func selectRerun(run ghRun, jobs []ghJob, ignore map[string]bool) rerunPlan {
	if blocker := runBlocker(run); blocker != "" {
		return rerunPlan{reason: blocker}
	}
	set, blocker := retryableJobs(jobs, ignore)
	if blocker != "" {
		return rerunPlan{reason: blocker}
	}
	if len(set.names) == 0 {
		return rerunPlan{reason: "no failed jobs outside the ignored ones"}
	}
	return rerunPlan{rerun: true, reason: set.reason(), jobs: set.names}
}

type retryableSet struct {
	names    []string
	noRunner bool
	amxCrash bool
}

func (s retryableSet) reason() string {
	switch {
	case s.noRunner && s.amxCrash:
		return mixedReason
	case s.amxCrash:
		return amxCrashReason
	default:
		return noRunnerReason
	}
}

// retryableJobs lists the failed jobs, or describes the first failure that is
// neither a macOS job cancelled before getting a runner nor an AMX crash.
func retryableJobs(jobs []ghJob, ignore map[string]bool) (retryableSet, string) {
	var set retryableSet
	for _, job := range jobs {
		if ignore[job.Name] || passed(job.Conclusion) {
			continue
		}
		switch {
		case noRunnerMacOS(job):
			set.noRunner = true
		case job.amxCrash:
			set.amxCrash = true
		default:
			return retryableSet{}, job.describe()
		}
		set.names = append(set.names, job.Name)
	}
	return set, ""
}

func (job ghJob) describe() string {
	name, conclusion := logSafe(job.Name), logSafe(job.Conclusion)
	runner, labels := logSafe(job.RunnerName), logSafe(job.Labels)
	return name + " ended " + conclusion + " on runner " + runner + " with labels " + labels
}

func passed(conclusion string) bool {
	return conclusion == "success" || conclusion == "skipped" || conclusion == "neutral"
}

func noRunnerMacOS(job ghJob) bool {
	return job.Conclusion == "cancelled" && job.RunnerName == "" &&
		slices.ContainsFunc(job.Labels, func(label string) bool {
			return strings.HasPrefix(label, "macos")
		})
}

// skipSuperseded keeps a pull request rerun from joining the PR's concurrency
// group and cancelling a run for a newer push.
func (c rerunCommand) skipSuperseded(
	ctx context.Context,
	opts rerunOptions,
	run ghRun,
	plan rerunPlan,
) (rerunPlan, error) {
	var latest bytes.Buffer
	err := c.gh(ctx, &latest, "-X", "GET",
		fmt.Sprintf("repos/%s/actions/workflows/%d/runs", opts.repo, run.WorkflowID),
		"-f", "branch="+run.HeadBranch, "-f", "event=pull_request", "-f", "per_page=1",
		"--jq", ".workflow_runs[0].id")
	if err != nil {
		return plan, err
	}
	if id := strings.TrimSpace(latest.String()); id != strconv.FormatInt(run.ID, 10) {
		reason := fmt.Sprintf(
			"a newer run %s exists for branch %s",
			logSafe(id),
			logSafe(run.HeadBranch),
		)
		return rerunPlan{reason: reason}, nil
	}
	return plan, nil
}

func (c rerunCommand) report(run ghRun, plan rerunPlan) {
	if !plan.rerun {
		_, _ = fmt.Fprintf(c.stdout, "run %d: no rerun: %s\n", run.ID, plan.reason)
		return
	}
	_, _ = fmt.Fprintf(
		c.stdout,
		"::notice::run %d: rerunning failed jobs: %s\n",
		run.ID,
		plan.reason,
	)
	for _, job := range plan.jobs {
		_, _ = fmt.Fprintf(c.stdout, "  %s\n", logSafe(job))
	}
}

func (c rerunCommand) runPath(opts rerunOptions) string {
	return fmt.Sprintf("repos/%s/actions/runs/%d", opts.repo, opts.runID)
}

func (c rerunCommand) firstAttemptJobs(ctx context.Context, opts rerunOptions) ([]ghJob, error) {
	var out bytes.Buffer
	path := c.runPath(opts) + "/attempts/1/jobs?per_page=100"
	if err := c.gh(ctx, &out, "--paginate", path, "--jq", ".jobs[]"); err != nil {
		return nil, err
	}
	var jobs []ghJob
	decoder := json.NewDecoder(&out)
	for {
		var job ghJob
		err := decoder.Decode(&job)
		if errors.Is(err, io.EOF) {
			return jobs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode jobs of run %d: %w", opts.runID, err)
		}
		jobs = append(jobs, job)
	}
}

func (c rerunCommand) getJSON(ctx context.Context, target any, path string) error {
	var out bytes.Buffer
	if err := c.gh(ctx, &out, path); err != nil {
		return err
	}
	if err := json.Unmarshal(out.Bytes(), target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func (c rerunCommand) gh(ctx context.Context, stdout io.Writer, args ...string) error {
	return ghAPI(ctx, c.runner, stdout, args...)
}

// ghAPI runs "gh api" and folds its escaped stderr into the error.
func ghAPI(ctx context.Context, runner commandRunner, stdout io.Writer, args ...string) error {
	if stdout == nil {
		stdout = io.Discard
	}
	var stderr bytes.Buffer
	err := runner.Run(ctx, commandSpec{
		Name:   "gh",
		Args:   append([]string{"api"}, args...),
		Stdout: stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return fmt.Errorf("%w: %s", err, logSafe(strings.TrimSpace(stderr.String())))
	}
	return nil
}

// logSafe quotes text that pull requests control so it cannot start a workflow
// command: %q escapes line breaks, and the runner's legacy parser matches
// "##[" anywhere in a line.
func logSafe(v any) string {
	return strings.ReplaceAll(fmt.Sprintf("%q", v), "#", `\x23`)
}
