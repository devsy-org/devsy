package git

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/log"
)

// lfsFilterMarker is the .gitattributes token that marks a path as LFS-tracked.
var lfsFilterMarker = []byte("filter=lfs")

// smudgeSkippedForClone reports whether the clone should set GIT_LFS_SKIP_SMUDGE.
func smudgeSkippedForClone(mode LFSMode) bool {
	if mode == LFSFull && !command.Exists(binGitLFS) {
		log.Info("git-lfs not found, skipping LFS smudge; LFS files will be pointer stubs")
	}
	return true
}

// SetupLFS configures Git LFS in the repository.
func (r *Repo) SetupLFS(ctx context.Context, mode LFSMode) {
	if mode == LFSSkip {
		return
	}
	if !repoUsesLFS(r.path) {
		return
	}

	if !command.Exists(binGitLFS) {
		if err := InstallLFS(ctx); err != nil {
			log.Warnf(
				"repository uses git-lfs but it could not be installed, LFS files will be pointer stubs: %v",
				err,
			)
			return
		}
	}

	if err := r.lfs(ctx, "install", "--local"); err != nil {
		log.Warnf("git-lfs install failed, LFS files may be pointer stubs: %v", err)
		return
	}

	if mode == LFSSetupOnly {
		return
	}

	if err := r.lfs(ctx, "pull"); err != nil {
		log.Warnf("git-lfs pull failed, LFS files may be pointer stubs: %v", err)
	}
}

// lfs runs a `git lfs <args...>` subcommand in the repository, returning any
// captured output alongside the error for diagnostics.
func (r *Repo) lfs(ctx context.Context, args ...string) error {
	res, err := r.run(ctx, append([]string{"lfs"}, args...)...)
	if err != nil {
		return fmt.Errorf("%w: %s%s", err, res.Stdout, res.Stderr)
	}
	return nil
}

// lfsDetectMaxDepth bounds how deep repoUsesLFS descends looking for .gitattributes.
const lfsDetectMaxDepth = 6

// repoUsesLFS reports whether any .gitattributes file within lfsDetectMaxDepth
// levels of the worktree declares an LFS filter.
func repoUsesLFS(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			if dirDepth(root, path) > lfsDetectMaxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != gitAttributesFile {
			return nil
		}
		// #nosec G304,G122 -- path is within the freshly cloned worktree we control
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if bytes.Contains(data, lfsFilterMarker) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// dirDepth returns how many directory levels dir is below root (root itself is 0).
func dirDepth(root, dir string) int {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}
