package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const bloblessCloneFilter = "--filter=blob:none"

var ErrRevisionPathNotFound = errors.New("path not found in git revision")

// Inspection is a lightweight, blobless, no-checkout clone used to inspect a
// small number of repository-owned configuration files before workspace build.
type Inspection struct {
	repo *Repo
	rev  string
	root string
}

// InspectRemote creates a temporary blobless clone and selects the exact
// revision described by info without checking out the worktree.
func InspectRemote(ctx context.Context, info *GitInfo, env []string) (*Inspection, error) {
	if info == nil || info.Repository == "" {
		return nil, fmt.Errorf("git repository is empty")
	}
	root, err := os.MkdirTemp("", "devsy-repo-inspect-*")
	if err != nil {
		return nil, err
	}

	repo, err := cloneInspectionRepo(ctx, root, info, env)
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	rev, err := selectInspectionRevision(ctx, repo, info)
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	return &Inspection{repo: repo, rev: rev, root: root}, nil
}

func cloneInspectionRepo(
	ctx context.Context,
	root string,
	info *GitInfo,
	env []string,
) (*Repo, error) {
	target := filepath.Join(root, "repo")
	args := []string{"clone", bloblessCloneFilter, "--no-checkout", "--depth=1"}
	if info.Branch != "" {
		args = append(args, "--branch", info.Branch)
	}
	args = append(args, info.Repository, target)

	bootstrap := At("", WithEnv(env))
	if _, err := bootstrap.runner.Run(ctx, RunOptions{Env: bootstrap.env, Args: args}); err != nil {
		return nil, fmt.Errorf("inspect remote repository: %w", err)
	}
	return At(target, WithEnv(env)), nil
}

func selectInspectionRevision(ctx context.Context, repo *Repo, info *GitInfo) (string, error) {
	if info.PR != "" {
		return fetchInspectionPR(ctx, repo, info.Repository, info.PR)
	}
	if info.Commit != "" {
		return fetchInspectionCommit(ctx, repo, info.Commit)
	}
	return "HEAD", nil
}

func fetchInspectionPR(
	ctx context.Context,
	repo *Repo,
	repository, request string,
) (string, error) {
	number := prNumber(request)
	if number == "" {
		return "", fmt.Errorf("invalid pull/merge request reference %q", request)
	}
	var lastErr error
	for _, host := range prCandidates(repository) {
		refspec := host.Refspec(number)
		if _, err := repo.run(ctx, "fetch", "--depth=1", "origin", refspec); err == nil {
			return "FETCH_HEAD", nil
		} else {
			lastErr = err
		}
	}
	return "", fmt.Errorf("fetch request revision: %w", lastErr)
}

func fetchInspectionCommit(ctx context.Context, repo *Repo, commit string) (string, error) {
	if _, err := repo.run(ctx, "fetch", "--depth=1", "origin", commit); err != nil {
		return "", fmt.Errorf("fetch commit %q: %w", commit, err)
	}
	return "FETCH_HEAD", nil
}

// ReadFile returns the bytes for a repository-relative path at the exact
// revision selected by InspectRemote.
func (i *Inspection) ReadFile(ctx context.Context, filePath string) ([]byte, error) {
	if i == nil || i.repo == nil {
		return nil, fmt.Errorf("git inspection is closed")
	}
	filePath = strings.TrimPrefix(strings.ReplaceAll(filePath, "\\", "/"), "./")
	object := i.rev + ":" + filePath
	if _, err := i.repo.run(ctx, "cat-file", "-e", object); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrRevisionPathNotFound, filePath)
	}
	result, err := i.repo.run(ctx, "show", object)
	if err != nil {
		return nil, fmt.Errorf("read %q from revision %s: %w", filePath, i.rev, err)
	}
	return append([]byte(nil), result.Stdout...), nil
}

func (i *Inspection) Revision() string {
	if i == nil {
		return ""
	}
	return i.rev
}

func (i *Inspection) Close() error {
	if i == nil || i.root == "" {
		return nil
	}
	root := i.root
	i.root = ""
	i.repo = nil
	return os.RemoveAll(root)
}
