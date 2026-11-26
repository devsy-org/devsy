package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const scopeZeroSHA = "0000000000000000000000000000000000000000"

// scopeDecision says whether a push to main needs the Go, e2e and
// microsandbox jobs. Light is only ever true when the diff is known and every
// changed file is documentation-like.
type scopeDecision struct {
	Light  bool
	Reason string
}

// pushHistory reads the commits and files of one push.
type pushHistory interface {
	Parents(ctx context.Context, sha string) ([]string, error)
	Fetch(ctx context.Context, sha string) error
	ChangedFiles(ctx context.Context, base, head string) ([]string, error)
}

func fullScope(reason string) scopeDecision {
	return scopeDecision{Reason: reason}
}

// classifyPush fails open: any missing or surprising input selects a full run.
func classifyPush(
	ctx context.Context,
	history pushHistory,
	selection SelectionConfig,
	before, sha string,
) scopeDecision {
	if before == "" || before == scopeZeroSHA || sha == "" {
		return fullScope("no previous commit to compare with")
	}
	parents, err := history.Parents(ctx, sha)
	if err != nil {
		return fullScope("cannot read the pushed commit")
	}
	if len(parents) != 1 || parents[0] != before {
		return fullScope("push is not one commit on top of the previous main commit")
	}
	if err := history.Fetch(ctx, before); err != nil {
		return fullScope("cannot fetch the previous main commit")
	}
	files, err := history.ChangedFiles(ctx, before, sha)
	if err != nil {
		return fullScope("cannot list the changed files")
	}
	if len(files) == 0 {
		return fullScope("no changed files found")
	}
	for _, file := range files {
		if !isLightFile(selection, file) {
			return fullScope(strconv.Quote(file) + " can affect Go tests")
		}
	}
	return scopeDecision{Light: true, Reason: fmt.Sprintf("%d changed files are documentation only", len(files))}
}

func isLightFile(selection SelectionConfig, file string) bool {
	return matchesAny([]string{file}, selection.LightRunPaths) &&
		!matchesAny([]string{file}, selection.LightRunExcludes)
}

type gitHistory struct {
	runner commandRunner
	dir    string
}

func (g gitHistory) git(ctx context.Context, args ...string) (string, error) {
	var stdout bytes.Buffer
	err := g.runner.Run(ctx, commandSpec{
		Name: "git", Args: args, Dir: g.dir, Stdout: &stdout, Stderr: io.Discard,
	})
	return stdout.String(), err
}

func (g gitHistory) Parents(ctx context.Context, sha string) ([]string, error) {
	out, err := g.git(ctx, "cat-file", "-p", sha)
	if err != nil {
		return nil, err
	}
	var parents []string
	for line := range strings.SplitSeq(out, "\n") {
		if line == "" {
			break
		}
		if parent, ok := strings.CutPrefix(line, "parent "); ok {
			parents = append(parents, parent)
		}
	}
	return parents, nil
}

func (g gitHistory) Fetch(ctx context.Context, sha string) error {
	_, err := g.git(ctx, "fetch", "--no-tags", "--depth=1", "origin", sha)
	return err
}

func (g gitHistory) ChangedFiles(ctx context.Context, base, head string) ([]string, error) {
	out, err := g.git(ctx, "diff", "--name-only", "--no-renames", "-z", base, head)
	if err != nil {
		return nil, err
	}
	var files []string
	for file := range strings.SplitSeq(out, "\x00") {
		if file != "" {
			files = append(files, file)
		}
	}
	return files, nil
}

func scopeCLI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scope", flag.ContinueOnError)
	fs.SetOutput(stderr)
	before := fs.String("before", "", "commit main pointed at before the push")
	sha := fs.String("sha", "", "commit main points at after the push")
	dir := fs.String("repo-dir", ".", "git checkout to inspect")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	manifest, err := LoadManifest()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load manifest: %v\n", err)
		return 2
	}
	decision := classifyPush(
		context.Background(),
		gitHistory{runner: execRunner{}, dir: *dir},
		manifest.Selection,
		*before,
		*sha,
	)
	_, _ = fmt.Fprintf(stdout, "light=%t\nreason=%s\n", decision.Light, decision.Reason)
	_, _ = fmt.Fprintf(
		stderr, "::notice::main run scope light=%t: %s\n", decision.Light, decision.Reason,
	)
	return 0
}
