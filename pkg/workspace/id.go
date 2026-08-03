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
		str = idFromRepoPath(str, splitted)
	}

	str = workspaceIDRegEx2.ReplaceAllString(workspaceIDRegEx1.ReplaceAllString(str, "-"), "")
	if len(str) > 48 {
		str = str[:48]
	}

	return strings.Trim(str, "-")
}

// idFromPROrBranch derives the ID from the "@..." suffix of a "repo@ref"
// string: a PR reference if str matches one, otherwise a branch name if it
// looks like one, falling back to the repo part (splitted[0]).
func idFromPROrBranch(str string, splitted []string) string {
	// 1. Check if PR was specified
	if prReferenceRegEx.MatchString(str) {
		return prReferenceRegEx.ReplaceAllStringFunc(splitted[1], git.GetIDForPR)
	}
	// 2. Check if a branch name has been specified, if so use this for the ID
	branch := strings.TrimSuffix(splitted[1], ".git")
	// Check if branch name matches expected regex
	if !branchRegEx.MatchString(branch) {
		return splitted[0]
	}
	return branch
}

// idFromRepoPath derives the ID from the final path segment of a repo URL
// with no recognized "@ref" suffix, stripping any trailing ".git".
func idFromRepoPath(str string, splitted []string) string {
	// Ensure we don't have a single trailing slash
	str = strings.TrimSuffix(str, "/")
	// 3. If not, then parse the repo name as ID
	index := strings.LastIndex(str, "/")
	if index == -1 {
		return str
	}
	str = str[index+1:]

	// remove a potential tag / branch name
	if len(splitted) == 2 && !branchRegEx.MatchString(splitted[1]) {
		str = splitted[0]
	}

	// remove .git if there is it
	return strings.TrimSuffix(str, ".git")
}
