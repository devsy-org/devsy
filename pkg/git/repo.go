package git

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/devsy-org/devsy/pkg/log"
)

// Repo is a handle to a git repository at a filesystem path.
type Repo struct {
	path   string
	env    []string
	runner Runner
}

// RepoOption configures a Repo.
type RepoOption func(*Repo)

// WithEnv appends extra environment entries applied to every operation.
func WithEnv(env []string) RepoOption {
	return func(r *Repo) {
		r.env = append(r.env, env...)
	}
}

// WithStrictHostKeyChecking appends the default SSH/terminal environment honoring the given policy.
func WithStrictHostKeyChecking(strict bool) RepoOption {
	return WithEnv(GetDefaultExtraEnv(strict))
}

// WithRunner injects a command Runner, primarily for testing.
func WithRunner(runner Runner) RepoOption {
	return func(r *Repo) {
		r.runner = runner
	}
}

// At returns a Repo rooted at path.
func At(path string, opts ...RepoOption) *Repo {
	r := &Repo{path: path, runner: defaultRunner}
	for _, opt := range opts {
		opt(r)
	}
	if r.runner == nil {
		r.runner = defaultRunner
	}
	return r
}

// Path returns the repository's filesystem path.
func (r *Repo) Path() string {
	return r.path
}

// ResetMode selects how Reset updates the index and working tree.
type ResetMode string

const (
	ResetSoft  ResetMode = "--soft"
	ResetMixed ResetMode = "--mixed"
	ResetHard  ResetMode = "--hard"
)

// Fetch fetches the given refspec from origin.
func (r *Repo) Fetch(ctx context.Context, refspec string) error {
	if err := r.runLogged(ctx, "fetch", "origin", refspec); err != nil {
		return fmt.Errorf("fetch %q: %w", refspec, err)
	}
	return nil
}

// Switch switches the working tree to an existing branch.
func (r *Repo) Switch(ctx context.Context, branch string) error {
	if _, err := r.run(ctx, "switch", branch); err != nil {
		return fmt.Errorf("switch to branch %q: %w", branch, err)
	}
	return nil
}

// CheckoutPR fetches a pull/merge request into a local branch and switches to
// it. The request number is taken from prRef; the remote refspec is resolved
// against repoURL's hosting provider, falling back to the other known
// conventions when the detected one has no such ref (e.g. self-hosted GitLab on
// a custom domain that URL detection can't recognize).
func (r *Repo) CheckoutPR(ctx context.Context, repoURL, prRef string) error {
	number := prNumber(prRef)
	if number == "" {
		return fmt.Errorf("not a pull/merge request reference: %q", prRef)
	}

	var lastErr error
	for _, host := range prCandidates(repoURL) {
		refspec := host.Refspec(number)
		prBranch := host.BranchName(number)
		log.Debugf("fetching %s request: %s", host.Name, refspec)

		err := r.Fetch(ctx, refspec+":"+prBranch)
		if err == nil {
			return r.Switch(ctx, prBranch)
		}
		if !isMissingRefError(err) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

// isMissingRefError reports whether err is git failing to find the requested
// remote ref, as opposed to an auth, network, or cancellation failure.
func isMissingRefError(err error) bool {
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		return false
	}
	msg := strings.ToLower(cmdErr.Stderr)
	return strings.Contains(msg, "couldn't find remote ref") ||
		strings.Contains(msg, "no such ref") ||
		strings.Contains(msg, "not found in upstream")
}

// Reset moves HEAD to commit using the given mode.
func (r *Repo) Reset(ctx context.Context, commit string, mode ResetMode) error {
	if err := r.runLogged(ctx, "reset", string(mode), commit); err != nil {
		return fmt.Errorf("reset head to %q: %w", commit, err)
	}
	return nil
}

// LsRemote reports whether the remote repository is reachable.
func (r *Repo) LsRemote(ctx context.Context, repository string) error {
	if _, err := r.run(ctx, "ls-remote", "--quiet", repository); err != nil {
		return fmt.Errorf("ls-remote %q: %w", repository, err)
	}
	return nil
}

// LsTree lists the files tracked at ref, recursively, as repo-relative paths.
func (r *Repo) LsTree(ctx context.Context, ref string) ([]string, error) {
	res, err := r.run(ctx, "ls-tree", "-r", "--full-name", "--name-only", ref)
	if err != nil {
		return nil, fmt.Errorf("ls-tree %q: %w", ref, err)
	}
	return splitLines(res.Stdout), nil
}

// Clone clones repository into the repo's path using the given options.
func (r *Repo) Clone(ctx context.Context, repository string, options ...Option) error {
	return r.cloneWith(ctx, repository, newCloneConfig(options...))
}

// CloneFromInfo clones the repository described by gitInfo into the repo's path,
// using the given credential helper and options.
func (r *Repo) CloneFromInfo(
	ctx context.Context,
	gitInfo *GitInfo,
	helper string,
	options ...Option,
) error {
	options = append(
		[]Option{WithBranch(gitInfo.Branch), WithCredentialHelper(helper)},
		options...,
	)
	c := newCloneConfig(options...)

	if err := r.cloneWith(ctx, gitInfo.Repository, c); err != nil {
		return err
	}

	switch {
	case gitInfo.PR != "":
		if err := r.CheckoutPR(ctx, gitInfo.Repository, gitInfo.PR); err != nil {
			return err
		}
	case gitInfo.Commit != "":
		if err := r.Reset(ctx, gitInfo.Commit, ResetHard); err != nil {
			return err
		}
	}

	// Bare clones have no worktree to hydrate.
	if c.strategy != BareCloneStrategy {
		r.SetupLFS(ctx, c.lfsMode)
	}
	return nil
}

// CredentialFill runs `git credential fill`, feeding request on stdin and
// returning git's response. Used to resolve credentials via configured helpers.
func (r *Repo) CredentialFill(ctx context.Context, request string) (string, error) {
	res, err := r.runner.Run(ctx, RunOptions{
		Dir:   r.path,
		Env:   r.env,
		Args:  []string{"credential", "fill"},
		Stdin: strings.NewReader(request),
	})
	if err != nil {
		return "", fmt.Errorf("git credential fill: %w", err)
	}
	return string(res.Stdout), nil
}

// run executes a git subcommand in the repository, capturing its output.
func (r *Repo) run(ctx context.Context, args ...string) (RunResult, error) {
	return r.runner.Run(ctx, RunOptions{
		Dir:  r.path,
		Env:  r.env,
		Args: args,
	})
}

// runLogged executes a git subcommand streaming its output to the logs.
func (r *Repo) runLogged(ctx context.Context, args ...string) error {
	w := log.Writer(log.LevelInfo)
	defer func() { _ = w.Close() }()

	_, err := r.runner.Run(ctx, RunOptions{
		Dir:    r.path,
		Env:    r.env,
		Args:   args,
		Stdout: w,
		Stderr: w,
	})
	return err
}

// cloneWith executes a `git clone` with the given config, streaming output to the logs.
func (r *Repo) cloneWith(ctx context.Context, repository string, c cloneConfig) error {
	w := log.Writer(log.LevelInfo)
	defer func() { _ = w.Close() }()

	env := append([]string{}, r.env...)
	if smudgeSkippedForClone(c.lfsMode) {
		env = append(env, "GIT_LFS_SKIP_SMUDGE=1")
	}

	_, err := r.runner.Run(ctx, RunOptions{
		Env:    env,
		Args:   c.args(repository, r.path),
		Stdout: w,
		Stderr: w,
	})
	return err
}

// splitLines splits command output into non-empty trimmed lines.
func splitLines(b []byte) []string {
	var lines []string
	for line := range strings.SplitSeq(string(b), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}
