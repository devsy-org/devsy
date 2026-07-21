package git

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/command"
)

const (
	CommitDelimiter string = "@sha256:"
	// PullRequestReference matches a pull-request (GitHub) or merge-request
	// (GitLab) refspec, capturing the request number.
	PullRequestReference string = "(?:pull|merge-requests)/([0-9]+)/head"
	SubPathDelimiter     string = "@subpath:"
	repoBaseRegEx        string = `((?:(?:https?|git|ssh|file):\/\/)?\/?(?:[^@\/\n]+@)?` +
		`(?:[^:\/\n]+)(?:[:\/][^\/\n]+)+(?:\.git)?)`
)

// WARN: Keep these in sync with the ref parsing in desktop's workspace-source.ts.
var (
	branchRegEx = regexp.MustCompile(`^` + repoBaseRegEx + `@([a-zA-Z0-9\./\-\_]+)$`)
	commitRegEx = regexp.MustCompile(
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
// suffixed with @branch, @subpath:<path>, @sha256:<commit>, or a pull/merge
// request ref (@pull/N/head or @merge-requests/N/head).
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

func PingRepository(str string, extraEnv []string) bool {
	if !command.Exists("git") {
		return false
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	return At("", WithEnv(extraEnv)).LsRemote(timeoutCtx, str) == nil
}

// GetBranchNameForPR returns the local branch name for a request ref, using the
// provider convention encoded in the ref (PR<n> for GitHub, MR<n> for GitLab).
func GetBranchNameForPR(ref string) string {
	number := prNumber(ref)
	if number == "" {
		return ref
	}
	return hostForRef(ref).BranchName(number)
}

// GetIDForPR returns the lowercased request identifier used in workspace IDs.
func GetIDForPR(ref string) string {
	number := prNumber(ref)
	if number == "" {
		return ref
	}
	return strings.ToLower(hostForRef(ref).BranchName(number))
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
