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

// Inspection is a blobless, no-checkout clone used to inspect a
// repository-owned configuration files before workspace build.
type Inspection struct {
	repo    *Repo
	rev     string
	root    string
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
	commitSHA, err := resolveCommitSHA(ctx, repo, rev)
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	subPath, err := cleanInspectionSubPath(info.SubPath)
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	return &Inspection{repo: repo, rev: commitSHA, root: root, subPath: subPath}, nil
}

func resolveCommitSHA(ctx context.Context, repo *Repo, rev string) (string, error) {
	revResult, err := repo.run(ctx, "rev-parse", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve immutable commit sha: %w", err)
	}
	commitSHA := strings.TrimSpace(string(revResult.Stdout))
	if commitSHA == "" {
		return "", fmt.Errorf("empty commit sha for revision %q", rev)
	}
	return commitSHA, nil
}

func cloneInspectionRepo(
	ctx context.Context,
	root string,
	info *GitInfo,
	env []string,
) (*Repo, error) {
	target := filepath.Join(root, "repo")
	args := []string{"clone", bloblessCloneFilter, "--no-checkout"}
	if info.Commit == "" {
		args = append(args, "--depth=1")
	}
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
	candidates := prCandidates(repository)
	if len(candidates) == 0 {
		return "", fmt.Errorf("unsupported repository host for pull/merge request %q", repository)
	}
	var lastErr error
	for _, host := range candidates {
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
	if _, err := repo.run(
		ctx,
		"fetch",
		"origin",
		"+refs/heads/*:refs/remotes/origin/*",
	); err == nil {
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
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if err := validateRepoRelativePath(kind, value); err != nil {
		return "", err
	}
	value = strings.TrimPrefix(strings.ReplaceAll(value, `\`, "/"), "/")
	clean := path.Clean(value)
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("git %s %q escapes the repository root", kind, value)
	}
	return clean, nil
}

func validateRepoRelativePath(kind, value string) error {
	if filepath.VolumeName(value) != "" || isWindowsAbs(value) {
		return fmt.Errorf("git %s %q must be relative to the repository root", kind, value)
	}
	if kind == "file path" && (strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\`)) {
		return fmt.Errorf("git %s %q must be relative to the repository root", kind, value)
	}
	return nil
}

func isWindowsAbs(value string) bool {
	if len(value) >= 2 && value[1] == ':' {
		c := value[0]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return true
		}
	}
	return strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, `//`)
}

// ReadDevContainerConfig locates and reads the devcontainer.json configuration
// from the inspection repository at its pinned revision, searching explicit paths,
// standard root locations (.devcontainer/devcontainer.json, .devcontainer.json),
// and profile-specific paths (.devcontainer/<id>/devcontainer.json).
func (i *Inspection) ReadDevContainerConfig(
	ctx context.Context,
	devContainerPath, devContainerID string,
) ([]byte, string, error) {
	if i == nil || i.repo == nil {
		return nil, "", fmt.Errorf("git inspection is closed")
	}
	if devContainerPath != "" {
		return i.readExplicitDevContainerConfig(ctx, devContainerPath)
	}
	if devContainerID != "" {
		return i.readProfileDevContainerConfig(ctx, devContainerID)
	}
	if data, pathFound, err := i.readRootDevContainerConfig(
		ctx,
	); err == nil ||
		!errors.Is(err, ErrRevisionPathNotFound) {
		return data, pathFound, err
	}
	return i.readNestedDevContainerConfig(ctx)
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

func (i *Inspection) readExplicitDevContainerConfig(
	ctx context.Context,
	devContainerPath string,
) ([]byte, string, error) {
	clean, err := cleanRepoRelativePath("devcontainer path", devContainerPath)
	if err != nil {
		return nil, "", err
	}
	data, err := i.ReadFile(ctx, clean)
	if err != nil {
		return nil, "", err
	}
	return data, clean, nil
}

func (i *Inspection) readProfileDevContainerConfig(
	ctx context.Context,
	devContainerID string,
) ([]byte, string, error) {
	cleanID, err := cleanRepoRelativePath("devcontainer id", devContainerID)
	if err != nil {
		return nil, "", err
	}
	idPath := path.Join(".devcontainer", cleanID, "devcontainer.json")
	data, err := i.ReadFile(ctx, idPath)
	if err == nil {
		return data, idPath, nil
	}
	if !errors.Is(err, ErrRevisionPathNotFound) {
		return nil, "", err
	}
	return nil, "", fmt.Errorf("devcontainer with ID %q not found in repository", devContainerID)
}

func (i *Inspection) readRootDevContainerConfig(ctx context.Context) ([]byte, string, error) {
	for _, candidate := range []string{
		path.Join(".devcontainer", "devcontainer.json"),
		".devcontainer.json",
	} {
		data, err := i.ReadFile(ctx, candidate)
		if err == nil {
			return data, candidate, nil
		}
		if !errors.Is(err, ErrRevisionPathNotFound) {
			return nil, "", err
		}
	}
	return nil, "", ErrRevisionPathNotFound
}

func (i *Inspection) readNestedDevContainerConfig(ctx context.Context) ([]byte, string, error) {
	nested := i.listDevContainerConfigs(ctx)
	if len(nested) == 1 {
		data, err := i.ReadFile(ctx, nested[0])
		if err != nil {
			return nil, "", err
		}
		return data, nested[0], nil
	}
	if len(nested) > 1 {
		return nil, "", fmt.Errorf("multiple devcontainer configurations found: %v", nested)
	}
	return nil, "", ErrRevisionPathNotFound
}

func (i *Inspection) listDevContainerConfigs(ctx context.Context) []string {
	treePath := ".devcontainer"
	if i.subPath != "" {
		treePath = path.Join(i.subPath, treePath)
	}
	targetTree := i.rev + ":" + treePath
	result, err := i.repo.run(ctx, "ls-tree", "--name-only", targetTree)
	if err != nil {
		return nil
	}
	entries := strings.Split(strings.TrimSpace(string(result.Stdout)), "\n")
	var matches []string
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		cand := path.Join(".devcontainer", entry, "devcontainer.json")
		fullCand := cand
		if i.subPath != "" {
			fullCand = path.Join(i.subPath, cand)
		}
		if _, err := i.repo.run(ctx, "cat-file", "-e", i.rev+":"+fullCand); err == nil {
			matches = append(matches, cand)
		}
	}
	return matches
}
