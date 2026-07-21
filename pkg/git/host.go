package git

import (
	"regexp"
	"strings"
)

var prNumberRegEx = regexp.MustCompile(`(?:pull|merge-requests)/([0-9]+)/head`)

const (
	hostNameGitHub = "github"
	hostNameGitLab = "gitlab"
)

// Host is a git provider's convention for referencing pull/merge requests:
// GitHub exposes them at pull/N/head, GitLab at merge-requests/N/head.
type Host struct {
	Name       string
	refPrefix  string
	branchAbbr string
	hostHint   string // substring identifying the provider in a repository URL
}

var (
	HostGitHub = Host{
		Name:       hostNameGitHub,
		refPrefix:  "pull",
		branchAbbr: "PR",
		hostHint:   hostNameGitHub,
	}
	HostGitLab = Host{
		Name:       hostNameGitLab,
		refPrefix:  "merge-requests",
		branchAbbr: "MR",
		hostHint:   hostNameGitLab,
	}

	// GitHub first: it is both the detection default and the first fallback.
	knownHosts = []Host{HostGitHub, HostGitLab}
)

func (h Host) Refspec(number string) string {
	return h.refPrefix + "/" + number + "/head"
}

func (h Host) BranchName(number string) string {
	return h.branchAbbr + number
}

// DetectHost picks the provider from a repository URL, defaulting to GitHub.
// It is best-effort: self-hosted instances on custom domains won't be
// recognized, which is why the fetch path falls back to the other convention.
func DetectHost(repoURL string) Host {
	// Match only the hostname so an owner or path segment (e.g. a GitHub repo
	// named "gitlab-ci") cannot masquerade as another provider.
	host := strings.ToLower(hostFromURL(repoURL))
	for _, h := range knownHosts {
		if strings.Contains(host, h.hostHint) {
			return h
		}
	}
	return HostGitHub
}

// hostFromURL extracts the hostname from SSH (git@host:path) and URL
// (scheme://[user@]host/path) forms, returning "" when none is present.
func hostFromURL(repoURL string) string {
	s := repoURL
	if i := strings.Index(s, "://"); i != -1 {
		s = s[i+3:]
	}
	if i := strings.Index(s, "@"); i != -1 {
		s = s[i+1:]
	}
	if i := strings.IndexAny(s, "/:"); i != -1 {
		s = s[:i]
	}
	return s
}

// hostForRef infers the provider from the ref itself, which already encodes the
// convention exactly (unlike DetectHost's URL heuristic).
func hostForRef(ref string) Host {
	if strings.Contains(ref, HostGitLab.refPrefix+"/") {
		return HostGitLab
	}
	return HostGitHub
}

func prNumber(ref string) string {
	if m := prNumberRegEx.FindStringSubmatch(ref); m != nil {
		return m[1]
	}
	return ""
}

// prCandidates orders the hosts to try for a checkout: detected host first,
// remaining conventions as fallbacks.
func prCandidates(repoURL string) []Host {
	primary := DetectHost(repoURL)
	candidates := []Host{primary}
	for _, h := range knownHosts {
		if h.Name != primary.Name {
			candidates = append(candidates, h)
		}
	}
	return candidates
}
