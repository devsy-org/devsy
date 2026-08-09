package agent

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/version"
	"github.com/stretchr/testify/assert"
)

func TestApplyURLDefaults_NormalizesTagURL(t *testing.T) {
	t.Setenv(config.EnvAgentURL, "")

	opts := &InjectOptions{
		DownloadURL: config.GitHubReleasesURL + "/releases/tag/v1.2.3",
	}
	opts.applyURLDefaults()

	assert.Equal(t, config.GitHubReleasesURL+"/releases/download/v1.2.3", opts.DownloadURL)
}

func TestApplyURLDefaults_PreservesNonTagURL(t *testing.T) {
	t.Setenv(config.EnvAgentURL, "")

	custom := "http://localhost:8080/devsy-linux-amd64"
	opts := &InjectOptions{DownloadURL: custom}
	opts.applyURLDefaults()

	assert.Equal(t, custom, opts.DownloadURL)
}

func TestApplyURLDefaults_FillsFromEnvWhenEmpty(t *testing.T) {
	override := "http://localhost:8080"
	t.Setenv(config.EnvAgentURL, override)

	opts := &InjectOptions{}
	opts.applyURLDefaults()

	assert.Equal(t, override, opts.DownloadURL)
}

func TestApplyPreferDownloadDefaults_CustomAgentURL(t *testing.T) {
	t.Setenv(config.EnvAgentURL, "http://localhost:8080")
	t.Setenv(config.EnvAgentPreferDownload, "")

	opts := &InjectOptions{DownloadURL: "http://localhost:8080"}
	opts.applyPreferDownloadDefaults()

	assert.NotNil(t, opts.PreferDownloadFromRemoteUrl)
	assert.True(t, *opts.PreferDownloadFromRemoteUrl)
	assert.True(t, opts.SkipVersionCheck)
}

func TestApplyPreferDownloadDefaults_EnvTrue(t *testing.T) {
	t.Setenv(config.EnvAgentURL, "")
	t.Setenv(config.EnvAgentPreferDownload, "true")

	opts := &InjectOptions{}
	opts.applyPreferDownloadDefaults()

	assert.NotNil(t, opts.PreferDownloadFromRemoteUrl)
	assert.True(t, *opts.PreferDownloadFromRemoteUrl)
	assert.True(t, opts.SkipVersionCheck)
}

func TestApplyPreferDownloadDefaults_EnvFalse(t *testing.T) {
	t.Setenv(config.EnvAgentURL, "")
	t.Setenv(config.EnvAgentPreferDownload, "false")

	opts := &InjectOptions{}
	opts.applyPreferDownloadDefaults()

	assert.NotNil(t, opts.PreferDownloadFromRemoteUrl)
	assert.False(t, *opts.PreferDownloadFromRemoteUrl)
	assert.True(t, opts.SkipVersionCheck)
}

func TestApplyPreferDownloadDefaults_EnvInvalidDefaultsTrue(t *testing.T) {
	t.Setenv(config.EnvAgentURL, "")
	t.Setenv(config.EnvAgentPreferDownload, "not-a-bool")

	opts := &InjectOptions{}
	opts.applyPreferDownloadDefaults()

	assert.NotNil(t, opts.PreferDownloadFromRemoteUrl)
	assert.True(t, *opts.PreferDownloadFromRemoteUrl)
	assert.True(t, opts.SkipVersionCheck)
}

func TestApplyPreferDownloadDefaults_DevBuildPrefersLocal(t *testing.T) {
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
}

func TestApplyPreferDownloadDefaults_ExplicitPreferencePreserved(t *testing.T) {
	t.Setenv(config.EnvAgentURL, "http://localhost:8080")
	t.Setenv(config.EnvAgentPreferDownload, "false")

	opts := &InjectOptions{PreferDownloadFromRemoteUrl: new(true)}
	opts.applyPreferDownloadDefaults()

	assert.True(t, *opts.PreferDownloadFromRemoteUrl)
	assert.False(t, opts.SkipVersionCheck)
}
