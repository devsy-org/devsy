package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// tipClient reads and starts runs of main's tip.
type tipClient interface {
	runHistory
	Tip(ctx context.Context) (string, error)
	Dispatch(ctx context.Context) error
}

// ensureTipRun starts a run for main's tip when the run that is starting is
// for an older commit and the tip has no run that was not cancelled.
//
// Pushes to main share one concurrency group, and GitHub does not promise that
// a waiting run is replaced in push order. An older push can therefore replace
// the run of the newest commit before that run starts. Every run that does
// start checks the tip, so the newest commit is tested whatever the order was.
func ensureTipRun(ctx context.Context, client tipClient, sha string) (string, error) {
	tip, err := client.Tip(ctx)
	if err != nil {
		return "", fmt.Errorf("read main's tip: %w", err)
	}
	if tip == sha {
		return "this run is for main's tip", nil
	}
	conclusions, err := client.Conclusions(ctx, tip)
	if err != nil {
		return "", fmt.Errorf("read the runs of main's tip: %w", err)
	}
	if slices.ContainsFunc(conclusions, notCancelled) {
		return "main's tip " + tip + " already has a run", nil
	}
	if err := client.Dispatch(ctx); err != nil {
		return "", fmt.Errorf("start a run for main's tip: %w", err)
	}
	return "started a run for main's tip " + tip, nil
}

func notCancelled(conclusion string) bool {
	return conclusion != cancelledState
}

func (g ghRunHistory) Tip(ctx context.Context) (string, error) {
	if g.repo == "" {
		return "", errNoRepository
	}
	var out bytes.Buffer
	path := fmt.Sprintf("repos/%s/git/ref/heads/%s", g.repo, mainBranch)
	if err := ghAPI(ctx, g.runner, &out, path, "--jq", ".object.sha"); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

func (g ghRunHistory) Dispatch(ctx context.Context) error {
	if g.repo == "" {
		return errNoRepository
	}
	path := fmt.Sprintf("repos/%s/actions/workflows/%s/dispatches", g.repo, scopeWorkflowFile)
	return ghAPI(ctx, g.runner, nil, "--method", "POST", path, "-f", "ref="+mainBranch)
}

func ensureTipCLI(args []string, stdout, stderr io.Writer) int {
	return ensureTipWith(execRunner{}, args, stdout, stderr)
}

func ensureTipWith(runner commandRunner, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ensure-tip", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sha := fs.String("sha", "", "commit the running workflow was started for")
	repo := fs.String("repo", os.Getenv("GITHUB_REPOSITORY"), "owner/name whose runs to read")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *sha == "" {
		_, _ = fmt.Fprintln(stderr, "--sha is required")
		return 2
	}
	message, err := ensureTipRun(
		context.Background(), ghRunHistory{runner: runner, repo: *repo}, *sha,
	)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "::warning::%v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "%s\n", message)
	return 0
}
