package workspace

import (
	"fmt"

	"github.com/devsy-org/devsy/pkg/provider"
)

// describeSource condenses a WorkspaceSource into a single human-readable line,
// picking the populated variant: git, then local folder, image, or container.
func describeSource(src provider.WorkspaceSource) string {
	switch {
	case src.GitRepository != "":
		return describeGitSource(src)
	case src.LocalFolder != "":
		return src.LocalFolder
	case src.Image != "":
		return src.Image
	case src.Container != "":
		return src.Container
	default:
		return ""
	}
}

// describeGitSource renders the git variant of a WorkspaceSource, choosing the
// most specific ref (branch, then commit, then PR) and appending any subpath.
func describeGitSource(src provider.WorkspaceSource) string {
	out := provider.WorkspaceSourceGit + src.GitRepository
	switch {
	case src.GitBranch != "":
		out += "@" + src.GitBranch
	case src.GitCommit != "":
		out += "@" + src.GitCommit
	case src.GitPRReference != "":
		out += "@" + src.GitPRReference
	}
	if src.GitSubPath != "" {
		out += fmt.Sprintf(" (%s)", src.GitSubPath)
	}
	return out
}
