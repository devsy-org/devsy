package workspace

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/devsy-org/devsy/pkg/git"
)

var (
	workspaceIDRegEx1 = regexp.MustCompile(`[^\w\-]`)
	workspaceIDRegEx2 = regexp.MustCompile(`[^0-9a-z\-]+`)

	branchRegEx      = regexp.MustCompile(`[^a-zA-Z0-9\.\-]+`)
	prReferenceRegEx = regexp.MustCompile(git.PullRequestReference)
)

func ToID(str string) string {
	str = strings.ToLower(filepath.ToSlash(str))
	splitted := strings.Split(str, "@")
	if len(splitted) == 2 {
		str = idFromPROrBranch(str, splitted)
	} else {
		str = idFromRepoPath(str)
	}

	str = workspaceIDRegEx2.ReplaceAllString(workspaceIDRegEx1.ReplaceAllString(str, "-"), "")
	if len(str) > 48 {
		str = str[:48]
	}

	return strings.Trim(str, "-")
}

// idFromPROrBranch derives the ID from the "@..." suffix of a "repo@ref"
// string: a PR reference if str matches one, otherwise a branch name if it
// looks like one, falling back to the repo part.
func idFromPROrBranch(str string, splitted []string) string {
	if prReferenceRegEx.MatchString(str) {
		return prReferenceRegEx.ReplaceAllStringFunc(splitted[1], git.GetIDForPR)
	}
	branch := strings.TrimSuffix(splitted[1], ".git")
	if !branchRegEx.MatchString(branch) {
		return splitted[0]
	}
	return branch
}

// idFromRepoPath derives the ID from the final path segment of a repo URL
// with no recognized "@ref" suffix, stripping any trailing ".git".
func idFromRepoPath(str string) string {
	str = strings.TrimSuffix(str, "/")
	index := strings.LastIndex(str, "/")
	if index == -1 {
		return str
	}
	str = str[index+1:]
	return strings.TrimSuffix(str, ".git")
}
