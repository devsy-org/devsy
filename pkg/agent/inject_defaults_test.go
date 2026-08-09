package agent

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/version"
	"github.com/stretchr/testify/assert"
)

// TestApplyURLDefaults_NormalizesTagURL covers the GitHub "/releases/tag/" ->
// "/releases/download/" normalization applied to a caller-supplied download URL.
func TestApplyURLDefaults_NormalizesTagURL(t *testing.T) {
	t.Setenv(config.EnvAgentURL, "")

	opts := &InjectOptions{
		DownloadURL: config.GitHubReleasesURL + "/releases/tag/v1.2.3",
	}
	opts.applyURLDefaults()

	assert.Equal(t, config.GitHubReleasesURL+"/releases/download/v1.2.3", opts.DownloadURL)
}

// TestApplyURLDefaults_PreservesNonTagURL ensures non-tag URLs pass through unchanged.
func TestApplyURLDefaults_PreservesNonTagURL(t *testing.T) {
	t.Setenv(config.EnvAgentURL, "")

	custom := "http://localhost:8080/devsy-linux-amd64"
	opts := &InjectOptions{DownloadURL: custom}
	opts.applyURLDefaults()

	assert.Equal(t, custom, opts.DownloadURL)
}

// TestApplyURLDefaults_FillsFromEnvWhenEmpty verifies an empty DownloadURL falls
// back to DefaultAgentDownloadURL, which honors DEVSY_AGENT_URL.
func TestApplyURLDefaults_FillsFromEnvWhenEmpty(t *testing.T) {
	override := "http://localhost:8080"
	t.Setenv(config.EnvAgentURL, override)

	opts := &InjectOptions{}
	opts.applyURLDefaults()

	assert.Equal(t, override, opts.DownloadURL)
}

// TestApplyPreferDownloadDefaults covers the DEVSY_AGENT_URL / DEVSY_AGENT_PREFER_DOWNLOAD
// side effects documented in AGENTS.md: a custom agent URL forces remote download and
// skips the version check, while the prefer-download env var overrides the heuristic.
func TestApplyPreferDownloadDefaults(t *testing.T) {
	t.Run("custom agent url prefers remote and skips version check", func(t *testing.T) {
		t.Setenv(config.EnvAgentURL, "http://localhost:8080")
		t.Setenv(config.EnvAgentPreferDownload, "")

		opts := &InjectOptions{DownloadURL: "http://localhost:8080"}
		opts.applyPreferDownloadDefaults()

		assert.NotNil(t, opts.PreferDownloadFromRemoteUrl)
		assert.True(t, *opts.PreferDownloadFromRemoteUrl)
		assert.True(t, opts.SkipVersionCheck)
	})

	t.Run("prefer download env true", func(t *testing.T) {
		t.Setenv(config.EnvAgentURL, "")
		t.Setenv(config.EnvAgentPreferDownload, "true")

		opts := &InjectOptions{}
		opts.applyPreferDownloadDefaults()

		assert.NotNil(t, opts.PreferDownloadFromRemoteUrl)
		assert.True(t, *opts.PreferDownloadFromRemoteUrl)
		assert.True(t, opts.SkipVersionCheck)
	})

	t.Run("prefer download env false", func(t *testing.T) {
		t.Setenv(config.EnvAgentURL, "")
		t.Setenv(config.EnvAgentPreferDownload, "false")

		opts := &InjectOptions{}
		opts.applyPreferDownloadDefaults()

		assert.NotNil(t, opts.PreferDownloadFromRemoteUrl)
		assert.False(t, *opts.PreferDownloadFromRemoteUrl)
		assert.True(t, opts.SkipVersionCheck)
	})

	t.Run("prefer download env invalid defaults true", func(t *testing.T) {
		t.Setenv(config.EnvAgentURL, "")
		t.Setenv(config.EnvAgentPreferDownload, "not-a-bool")

		opts := &InjectOptions{}
		opts.applyPreferDownloadDefaults()

		assert.NotNil(t, opts.PreferDownloadFromRemoteUrl)
		assert.True(t, *opts.PreferDownloadFromRemoteUrl)
		assert.True(t, opts.SkipVersionCheck)
	})

	t.Run("default dev build prefers local", func(t *testing.T) {
		if version.GetVersion() != version.DevVersion {
			t.Skip("test assumes a dev build")
		}
		t.Setenv(config.EnvAgentURL, "")
		t.Setenv(config.EnvAgentPreferDownload, "")

		opts := &InjectOptions{}
		opts.ApplyDefaults()

		assert.NotNil(t, opts.PreferDownloadFromRemoteUrl)
		assert.False(t, *opts.PreferDownloadFromRemoteUrl)
		assert.True(t, opts.SkipVersionCheck)
	})

	t.Run("explicit preference preserved", func(t *testing.T) {
		t.Setenv(config.EnvAgentURL, "http://localhost:8080")
		t.Setenv(config.EnvAgentPreferDownload, "false")

		opts := &InjectOptions{PreferDownloadFromRemoteUrl: Bool(true)}
		opts.applyPreferDownloadDefaults()

		assert.True(t, *opts.PreferDownloadFromRemoteUrl)
		assert.False(t, opts.SkipVersionCheck)
	})
}
