package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/log"
)

const (
	CommitDelimiter      string = "@sha256:"
	PullRequestReference string = "pull/([0-9]+)/head"
	SubPathDelimiter     string = "@subpath:"
)

// WARN: Make sure this matches the regex in /desktop/src/views/Workspaces/CreateWorkspace/CreateWorkspaceInput.tsx!
var (
	// Updated regex pattern to support SSH-style Git URLs.
	repoBaseRegEx = `((?:(?:https?|git|ssh|file):\/\/)?\/?(?:[^@\/\n]+@)?(?:[^:\/\n]+)(?:[:\/][^\/\n]+)+(?:\.git)?)`
	branchRegEx   = regexp.MustCompile(`^` + repoBaseRegEx + `@([a-zA-Z0-9\./\-\_]+)$`)
	commitRegEx   = regexp.MustCompile(
		`^` + repoBaseRegEx + regexp.QuoteMeta(CommitDelimiter) + `([a-zA-Z0-9]+)$`,
	)
	prReferenceRegEx = regexp.MustCompile(`^` + repoBaseRegEx + `@(` + PullRequestReference + `)$`)
	subPathRegEx     = regexp.MustCompile(
		`^` + repoBaseRegEx + regexp.QuoteMeta(SubPathDelimiter) + `([a-zA-Z0-9\./\-\_]+)$`,
	)
)

// recognizedSchemes are the prefixes NormalizeRepository accepts without
// rewriting; anything else is treated as a bare host[/path] and prefixed
// with https://.
var recognizedSchemes = []string{"ssh://", "git@", "http://", "https://", "file://"}

// NormalizeRepository parses a repository reference into its structured parts.
// Accepts plain URLs, the "git:<url>" workspace-source scheme, and references
// suffixed with @branch, @subpath:<path>, @sha256:<commit>, or @pull/N/head.
// Bare host[/path] inputs are upgraded to https://.
func NormalizeRepository(str string) *GitInfo {
	str = canonicalizeURL(str)

	// PR references are mutually exclusive with branch/commit/subpath.
	if match := prReferenceRegEx.FindStringSubmatch(str); match != nil {
		return &GitInfo{Repository: match[1], PR: match[2]}
	}

	info := &GitInfo{Repository: str}
	if match := subPathRegEx.FindStringSubmatch(info.Repository); match != nil {
		info.Repository = match[1]
		info.SubPath = strings.TrimSuffix(match[2], "/")
	}
	if match := branchRegEx.FindStringSubmatch(info.Repository); match != nil {
		info.Repository = match[1]
		info.Branch = match[2]
	}
	if match := commitRegEx.FindStringSubmatch(info.Repository); match != nil {
		info.Repository = match[1]
		info.Commit = match[2]
	}
	return info
}

// canonicalizeURL strips the workspace-source "git:" scheme (the form
// WorkspaceSource.String emits; without this strip, a value that round-trips
// through workspace list → up becomes "https://git:https://...") and upgrades
// bare host[/path] inputs to https://.
func canonicalizeURL(str string) string {
	str = strings.TrimPrefix(str, "git:")
	for _, s := range recognizedSchemes {
		if strings.HasPrefix(str, s) {
			return str
		}
	}
	return "https://" + str
}

func CommandContext(ctx context.Context, extraEnv []string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	return cmd
}

func PingRepository(str string, extraEnv []string) bool {
	if !command.Exists("git") {
		return false
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	_, err := CommandContext(timeoutCtx, extraEnv, "ls-remote", "--quiet", str).CombinedOutput()
	return err == nil
}

func GetBranchNameForPR(ref string) string {
	regex := regexp.MustCompile(PullRequestReference)
	return regex.ReplaceAllString(ref, "PR${1}")
}

func GetIDForPR(ref string) string {
	regex := regexp.MustCompile(PullRequestReference)
	return regex.ReplaceAllString(ref, "pr${1}")
}

// GitInfo is the parsed form of a repository reference. Branch, Commit, PR,
// and SubPath are independent: a reference can carry zero or more of them.
// PR is exclusive with Branch and Commit.
type GitInfo struct {
	Repository string
	Branch     string
	Commit     string
	PR         string
	SubPath    string
}

func CloneRepository(
	ctx context.Context,
	gitInfo *GitInfo,
	targetDir string,
	helper string,
	strictHostKeyChecking bool,
	cloneOptions ...Option,
) error {
	return CloneRepositoryWithEnv(
		ctx,
		gitInfo,
		nil,
		targetDir,
		helper,
		strictHostKeyChecking,
		cloneOptions...)
}

func GetDefaultExtraEnv(strictHostKeyChecking bool) []string {
	newExtraEnv := []string{"GIT_TERMINAL_PROMPT=0"}
	sshArgs := "GIT_SSH_COMMAND=ssh -oBatchMode=yes -oStrictHostKeyChecking="
	if strictHostKeyChecking {
		sshArgs += "yes"
	} else {
		sshArgs += "no"
	}
	return append(newExtraEnv, sshArgs)
}

func CloneRepositoryWithEnv(
	ctx context.Context,
	gitInfo *GitInfo,
	extraEnv []string,
	targetDir string,
	helper string,
	strictHostKeyChecking bool,
	cloneOptions ...Option,
) error {
	cloner := NewClonerWithOpts(cloneOptions...)

	// make sure to append the extra env so that they override existing env vars if set
	extraEnv = append(GetDefaultExtraEnv(strictHostKeyChecking), extraEnv...)

	extraArgs := []string{}
	if helper != "" {
		extraArgs = append(extraArgs, "--config", "credential.helper="+helper)
	}

	if gitInfo.Branch != "" {
		extraArgs = append(extraArgs, "--branch", gitInfo.Branch)
	}

	if err := cloner.Clone(
		ctx,
		gitInfo.Repository,
		targetDir,
		extraArgs,
		extraEnv,
	); err != nil {
		return err
	}

	if gitInfo.PR != "" {
		return checkoutPR(ctx, gitInfo, extraEnv, targetDir)
	}

	if gitInfo.Commit != "" {
		return checkoutCommit(ctx, gitInfo, extraEnv, targetDir)
	}

	return nil
}

func checkoutPR(
	ctx context.Context,
	gitInfo *GitInfo,
	extraEnv []string,
	targetDir string,
) error {
	log.Debugf("Fetching pull request : %s", gitInfo.PR)

	prBranch := GetBranchNameForPR(gitInfo.PR)

	// Try to fetch the pull request by
	// checking out the reference GitHub set up for it. Afterwards, switch to it.
	// See [this doc](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/reviewing-changes-in-pull-requests/checking-out-pull-requests-locally#modifying-an-inactive-pull-request-locally)
	// Command args: `git fetch origin pull/996/head:PR996`
	fetchArgs := []string{"fetch", "origin", gitInfo.PR + ":" + prBranch}
	fetchCmd := CommandContext(ctx, extraEnv, fetchArgs...)
	fetchCmd.Dir = targetDir
	if err := fetchCmd.Run(); err != nil {
		return fmt.Errorf("fetch pull request reference: %w", err)
	}

	// git switch PR996
	switchArgs := []string{"switch", prBranch}
	switchCmd := CommandContext(ctx, extraEnv, switchArgs...)
	switchCmd.Dir = targetDir
	if err := switchCmd.Run(); err != nil {
		return fmt.Errorf("switch to branch: %w", err)
	}

	return nil
}

func checkoutCommit(
	ctx context.Context,
	gitInfo *GitInfo,
	extraEnv []string,
	targetDir string,
) error {
	stdout := log.Writer(log.LevelInfo)
	stderr := log.Writer(log.LevelError)
	defer func() { _ = stdout.Close() }()
	defer func() { _ = stderr.Close() }()

	args := []string{"reset", "--hard", gitInfo.Commit}
	gitCommand := CommandContext(ctx, extraEnv, args...)
	gitCommand.Dir = targetDir
	gitCommand.Stdout = stdout
	gitCommand.Stderr = stderr
	if err := gitCommand.Run(); err != nil {
		return fmt.Errorf("reset head to commit: %w", err)
	}

	return nil
}
