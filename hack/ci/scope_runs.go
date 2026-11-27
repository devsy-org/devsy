package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

const (
	scopeWorkflowFile = "pr-ci.yml"
	resultFailure     = "failure"
)

var errNoRepository = errors.New("no repository to read runs from")

// runHistory lists how the main runs of a commit ended. A run that has not
// finished reports "none".
type runHistory interface {
	Conclusions(ctx context.Context, sha string) ([]string, error)
}

// previousRunReason returns why the previous main commit does not prove its
// changes were tested, or "" when one of its runs finished and was not cancelled.
// Main pushes share one concurrency group, so a newer push replaces a run that
// is still waiting and its changes would otherwise never be tested.
func previousRunReason(ctx context.Context, runs runHistory, sha string) string {
	conclusions, err := runs.Conclusions(ctx, sha)
	if err != nil {
		return "cannot read the runs of the previous main commit"
	}
	if slices.ContainsFunc(conclusions, finishedUncancelled) {
		return ""
	}
	return "the previous main commit has no finished run, it may have been replaced in the queue"
}

func finishedUncancelled(conclusion string) bool {
	return conclusion == resultSuccess || conclusion == resultFailure
}

// requirePreviousRun turns a light decision into a full one unless the
// previous main commit was tested.
func requirePreviousRun(
	ctx context.Context,
	runs runHistory,
	push pushRange,
	decision scopeDecision,
) scopeDecision {
	if !decision.Light {
		return decision
	}
	if reason := previousRunReason(ctx, runs, push.Before); reason != "" {
		return fullScope(reason)
	}
	return decision
}

type ghRunHistory struct {
	runner commandRunner
	repo   string
}

func (g ghRunHistory) Conclusions(ctx context.Context, sha string) ([]string, error) {
	if g.repo == "" {
		return nil, errNoRepository
	}
	query := url.Values{
		"event":    {"push"},
		"branch":   {"main"},
		"head_sha": {sha},
		"per_page": {"100"},
	}
	path := fmt.Sprintf(
		"repos/%s/actions/workflows/%s/runs?%s", g.repo, scopeWorkflowFile, query.Encode(),
	)
	var out bytes.Buffer
	err := ghAPI(ctx, g.runner, &out, path, "--jq", `.workflow_runs[] | .conclusion // "none"`)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out.String()), nil
}
