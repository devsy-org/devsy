package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
)

const rootUser = "root"

var (
	gitLookPath = exec.LookPath
	gitCommand  = exec.CommandContext
)

// setupGitSafeDirectory registers the workspace as a Git safe.directory in system
// configuration for non-root container users to prevent dubious ownership errors.
func setupGitSafeDirectory(
	ctx context.Context,
	setupInfo *config.Result,
) error {
	user := config.GetRemoteUser(setupInfo)
	if user == "" || user == rootUser {
		log.Debugf("Git workspace safety: remote user is root; skipping")
		return nil
	}

	candidate := gitWorkspaceCandidate(setupInfo)
	if candidate == "" {
		log.Debugf("Git workspace safety: no Git repository at workspace mount target; skipping")
		return nil
	}

	if _, err := gitLookPath("git"); err != nil {
		log.Debugf("git not found; skipping safe.directory setup")
		return nil
	}

	return ensureGitSafeDirectoryEntry(ctx, candidate)
}

func ensureGitSafeDirectoryEntry(ctx context.Context, candidate string) error {
	entries, err := getGitSafeDirectories(ctx)
	if err != nil {
		return fmt.Errorf("read safe.directory entries: %w", err)
	}

	if alreadyHasSafeDirectory(entries, candidate) {
		return nil
	}

	if err := addGitSafeDirectory(ctx, candidate); err != nil {
		return err
	}

	return verifyGitSafeDirectory(ctx, candidate)
}

func alreadyHasSafeDirectory(entries []string, candidate string) bool {
	active := activeSafeDirectories(entries)
	if slices.Contains(active, "*") {
		log.Debugf("Git workspace safety: safe.directory already contains wildcard (*); skipping")
		return true
	}
	if slices.Contains(active, candidate) {
		log.Debugf("Git workspace safety: safe.directory already contains %s", candidate)
		return true
	}
	return false
}

func activeSafeDirectories(entries []string) []string {
	for i, entry := range slices.Backward(entries) {
		if entry == "" {
			return entries[i+1:]
		}
	}
	return entries
}

func addGitSafeDirectory(ctx context.Context, candidate string) error {
	addCmd := gitCommand(ctx, "git", "config", "--system", "--add", "safe.directory", candidate)
	addCmd.Dir = "/"
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf(
			"add git safe.directory %s: %s: %w",
			candidate,
			strings.TrimSpace(string(out)),
			err,
		)
	}
	log.Infof("configured Git safe.directory for Devsy workspace: %s", candidate)
	return nil
}

func verifyGitSafeDirectory(ctx context.Context, candidate string) error {
	rechecked, err := getGitSafeDirectories(ctx)
	if err != nil {
		return fmt.Errorf("verify git safe.directory %s: %w", candidate, err)
	}
	if !slices.Contains(activeSafeDirectories(rechecked), candidate) {
		return fmt.Errorf(
			"verification failed: safe.directory %s not found in system git config after write",
			candidate,
		)
	}
	return nil
}

// gitWorkspaceCandidate returns the Git root path, preferring the mount target over
// the container workspace folder.
func gitWorkspaceCandidate(setupInfo *config.Result) string {
	if setupInfo == nil || setupInfo.SubstitutionContext == nil {
		return ""
	}

	if setupInfo.SubstitutionContext.WorkspaceMount != "" {
		mount := config.ParseMount(setupInfo.SubstitutionContext.WorkspaceMount)
		if isValidGitWorkspace(mount.Target) {
			return filepath.Clean(mount.Target)
		}
	}

	if isValidGitWorkspace(setupInfo.SubstitutionContext.ContainerWorkspaceFolder) {
		return filepath.Clean(setupInfo.SubstitutionContext.ContainerWorkspaceFolder)
	}

	return ""
}

// isValidGitWorkspace validates that path is an absolute, non-root directory with a valid .git.
func isValidGitWorkspace(path string) bool {
	if !isValidWorkspacePath(path) {
		return false
	}
	clean := filepath.Clean(path)
	info, err := os.Lstat(clean)
	if err != nil || info.Mode().Type() != os.ModeDir {
		return false
	}
	return hasValidGitControlPath(clean)
}

func isValidWorkspacePath(path string) bool {
	if path == "" {
		return false
	}
	clean := filepath.Clean(path)
	return filepath.IsAbs(clean) && clean != "/" && clean != "/workspaces" &&
		filepath.Dir(clean) != clean && !strings.Contains(clean, "*")
}

func hasValidGitControlPath(dir string) bool {
	gitPath := filepath.Join(dir, ".git")
	gitInfo, err := os.Lstat(gitPath)
	if err != nil {
		return false
	}
	switch gitInfo.Mode().Type() {
	case os.ModeDir, 0:
		return true
	default:
		return false
	}
}

// getGitSafeDirectories returns all system safe.directory entries via NUL-delimited output.
func getGitSafeDirectories(ctx context.Context) ([]string, error) {
	cmd := gitCommand(ctx, "git", "config", "--system", "--null", "--get-all", "safe.directory")
	cmd.Dir = "/"
	out, err := cmd.Output()
	if err != nil {
		return handleGitConfigError(err)
	}
	return parseSafeDirectoryOutput(out), nil
}

func handleGitConfigError(err error) ([]string, error) {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return nil, nil
	}
	var stderrMsg string
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		stderrMsg = ": " + strings.TrimSpace(string(exitErr.Stderr))
	}
	return nil, fmt.Errorf("read git safe.directory%s: %w", stderrMsg, err)
}

func parseSafeDirectoryOutput(out []byte) []string {
	if len(out) == 0 {
		return nil
	}
	parts := bytes.Split(out, []byte{0})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	entries := make([]string, len(parts))
	for i, p := range parts {
		entries[i] = string(p)
	}
	return entries
}
