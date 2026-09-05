package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	bloblessCloneFilter = "--filter=blob:none"
	// inspectionHeadRev is the default revision selector used when the
	// inspection carries no explicit commit or PR reference.
	inspectionHeadRev = "HEAD"
)

var ErrRevisionPathNotFound = errors.New("path not found in git revision")

// Inspection is a lightweight, blobless, no-checkout clone used to inspect a
// small number of repository-owned configuration files before workspace build.
type Inspection struct {
	repo *Repo
	rev  string
	root string
	// subPath is the repository-relative directory that ReadFile treats as
	// the project root, mirroring info.SubPath (the @subpath: selector).
	// Empty means the repository root.
	subPath string
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
	subPath, err := cleanInspectionSubPath(info.SubPath)
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	return &Inspection{repo: repo, rev: rev, root: root, subPath: subPath}, nil
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
	return inspectionHeadRev, nil
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
		_, err := repo.run(ctx, "fetch", "--depth=1", "origin", refspec)
		if err == nil {
			return "FETCH_HEAD", nil
		}
		lastErr = err
	}
	return "", fmt.Errorf("fetch request revision: %w", lastErr)
}

func fetchInspectionCommit(ctx context.Context, repo *Repo, commit string) (string, error) {
	if _, err := repo.run(ctx, "cat-file", "-e", commit+"^{commit}"); err == nil {
		return commit, nil
	}
	if _, err := repo.run(ctx, "fetch", "--depth=1", "origin", commit); err == nil {
		return "FETCH_HEAD", nil
	}
	if _, err := repo.run(ctx, "fetch", "origin", "+refs/heads/*:refs/remotes/origin/*"); err == nil {
		if _, err := repo.run(ctx, "cat-file", "-e", commit+"^{commit}"); err == nil {
			return commit, nil
		}
	}
	if _, err := repo.run(ctx, "fetch", "--unshallow", "origin"); err == nil {
		if _, err := repo.run(ctx, "cat-file", "-e", commit+"^{commit}"); err == nil {
			return commit, nil
		}
	}
	return "", fmt.Errorf("fetch commit %q: commit not found in remote repository", commit)
}

// ReadFile returns the bytes for a path relative to the selected subpath
// project root (or the repository root when no subpath is set) at the exact
// revision selected by InspectRemote.
func (i *Inspection) ReadFile(ctx context.Context, filePath string) ([]byte, error) {
	if i == nil || i.repo == nil {
		return nil, fmt.Errorf("git inspection is closed")
	}
	cleanPath, err := cleanRepoRelativePath("file path", filePath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if cleanPath == "" {
		return nil, fmt.Errorf("read file: path must not be empty")
	}
	if i.subPath != "" {
		cleanPath = path.Join(i.subPath, cleanPath)
	}
	object := i.rev + ":" + cleanPath
	if _, err := i.repo.run(ctx, "cat-file", "-e", object); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrRevisionPathNotFound, cleanPath)
	}
	result, err := i.repo.run(ctx, "show", object)
	if err != nil {
		return nil, fmt.Errorf("read %q from revision %s: %w", cleanPath, i.rev, err)
	}
	return append([]byte(nil), result.Stdout...), nil
}

// cleanRepoRelativePath normalizes and validates a repository-relative path
// (either an @subpath: selector or a file path to read), rejecting anything
// absolute or that would escape the repository root once joined with
// another repository-relative path.
func cleanRepoRelativePath(kind, value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimPrefix(value, "/")
	if value == "" {
		return "", nil
	}
	clean := path.Clean(value)
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("git %s %q escapes the repository root", kind, value)
	}
	return clean, nil
}

// cleanInspectionSubPath normalizes and validates a repository-relative
// subpath selector (from an @subpath: reference), rejecting anything that
// would escape the repository root once joined with a file path.
func cleanInspectionSubPath(value string) (string, error) {
	return cleanRepoRelativePath("subpath", value)
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
