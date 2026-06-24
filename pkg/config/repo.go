package config

import (
	"os"
	"strings"

	"github.com/devsy-org/devsy/pkg/version"
)

const (
	RepoOwner         = "devsy-org"
	RepoName          = "devsy"
	RepoSlug          = RepoOwner + "/" + RepoName
	GitHubRepoURL     = "https://github.com/" + RepoSlug
	GitHubReleasesURL = GitHubRepoURL + "/releases"
	GitHubAPIUserURL  = "https://api.github.com/users/" + RepoOwner
	ProviderPrefix    = RepoName + "-provider-"

	// ProReleaseName is the Helm release / product name for Devsy Pro.
	ProReleaseName = RepoName + "-pro"

	// BinaryName is the CLI binary base name used in downloads and SSH host suffixes.
	BinaryName = RepoName

	// SSHHostSuffix is appended to workspace IDs for SSH config host entries.
	SSHHostSuffix = "." + BinaryName

	// WebsiteBaseURL is the project website.
	WebsiteBaseURL = "https://" + RepoName + ".sh"

	// WebsiteAssetsURL is the root URL for icon/image assets, served at WebsiteAssetsURL + "/<name>.svg".
	WebsiteAssetsURL = "https://assets." + RepoName + ".sh"

	// AgentDownloadBaseURL is the prefix under which versioned agent binaries are published.
	AgentDownloadBaseURL = GitHubReleasesURL + "/download/"

	// AgentLatestDownloadURL points at the floating "latest" agent release.
	AgentLatestDownloadURL = GitHubReleasesURL + "/latest/download"
)

// DefaultAgentDownloadURL returns the URL the host should download the agent
// binary from. Honors the DEVSY_AGENT_URL override; otherwise uses the
// version-pinned release URL, falling back to "latest" in dev builds.
func DefaultAgentDownloadURL() string {
	if override := os.Getenv(EnvAgentURL); override != "" {
		return strings.TrimRight(override, "/")
	}
	if version.GetVersion() == version.DevVersion {
		return AgentLatestDownloadURL
	}
	return AgentDownloadBaseURL + version.GetVersion()
}
