package git

import (
	"testing"

	"gotest.tools/assert"
	"gotest.tools/assert/cmp"
)

func TestDetectHost(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"git@github.com:org/repo.git", "github"},
		{"https://github.com/org/repo.git", "github"},
		{"git@gitlab.com:org/repo.git", "gitlab"},
		{"https://gitlab.example.com/org/repo.git", "gitlab"},
		{"https://git.internal.example/org/repo.git", "github"}, // unknown host defaults to GitHub
	}
	for _, c := range cases {
		assert.Check(t, cmp.Equal(c.want, DetectHost(c.url).Name), "url=%s", c.url)
	}
}

func TestHostRefspecAndBranch(t *testing.T) {
	assert.Equal(t, "pull/42/head", HostGitHub.Refspec("42"))
	assert.Equal(t, "PR42", HostGitHub.BranchName("42"))
	assert.Equal(t, "merge-requests/42/head", HostGitLab.Refspec("42"))
	assert.Equal(t, "MR42", HostGitLab.BranchName("42"))
}

func TestPRNumber(t *testing.T) {
	assert.Equal(t, "996", prNumber("pull/996/head"))
	assert.Equal(t, "7125", prNumber("merge-requests/7125/head"))
	assert.Equal(t, "", prNumber("refs/heads/main"))
}

func TestPRCandidatesOrder(t *testing.T) {
	got := prCandidates("git@gitlab.com:org/repo.git")
	assert.Equal(t, 2, len(got))
	assert.Equal(t, "gitlab", got[0].Name) // detected host first
	assert.Equal(t, "github", got[1].Name) // fallback
}
