package workspace

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
)

func TestClearInitialized(t *testing.T) {
	newConfig := func(providers map[string]*config.ProviderConfig) *config.Config {
		return &config.Config{
			DefaultContext: testDefaultContext,
			Contexts: map[string]*config.ContextConfig{
				testDefaultContext: {Providers: providers},
			},
		}
	}

	const updated, other = "updated-provider", "other-provider"

	t.Run("resets an initialized provider", func(t *testing.T) {
		cfg := newConfig(map[string]*config.ProviderConfig{
			updated: {Initialized: true},
		})

		clearInitialized(cfg, updated)

		assert.False(t, cfg.Current().Providers[updated].Initialized)
	})

	t.Run("leaves other providers untouched", func(t *testing.T) {
		cfg := newConfig(map[string]*config.ProviderConfig{
			updated: {Initialized: true},
			other:   {Initialized: true},
		})

		clearInitialized(cfg, updated)

		assert.True(t, cfg.Current().Providers[other].Initialized)
	})

	t.Run("no-ops for an unknown provider", func(t *testing.T) {
		cfg := newConfig(map[string]*config.ProviderConfig{})

		assert.NotPanics(t, func() { clearInitialized(cfg, "missing") })
	})
}

func TestShouldSkipProviderUpdate(t *testing.T) {
	tests := []struct {
		name         string
		isDevVersion bool
		isInternal   bool
		expected     bool
	}{
		{
			name:         "skip when dev version",
			isDevVersion: true,
			isInternal:   false,
			expected:     true,
		},
		{
			name:         "skip when internal",
			isDevVersion: false,
			isInternal:   true,
			expected:     true,
		},
		{
			name:         "skip when both dev version and internal",
			isDevVersion: true,
			isInternal:   true,
			expected:     true,
		},
		{
			name:         "do not skip for regular provider",
			isDevVersion: false,
			isInternal:   false,
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldSkipProviderUpdate(tt.isDevVersion, tt.isInternal)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestProviderVersionNeedsUpdate(t *testing.T) {
	tests := []struct {
		name, newVer, curVer string
		expected, expectErr  bool
	}{
		{"same version", "v0.5.0", "v0.5.0", false, false},
		{"newer version", "v0.6.0", "v0.5.0", true, false},
		{"older version (downgrade)", "v0.4.0", "v0.5.0", true, false},
		{"mixed v prefix", "v0.6.0", "0.5.0", true, false},
		{"patch difference", "v1.2.4", "v1.2.3", true, false},
		{"invalid new version", "not-a-version", "v0.5.0", false, true},
		{"invalid current version", "v0.5.0", "not-a-version", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := providerVersionNeedsUpdate(tt.newVer, tt.curVer)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestProviderUpdateSkipReasonResolvedGitHubSource(t *testing.T) {
	original := provider.ProviderSource{Github: "org/provider", Raw: "org/provider@v1.0.0"}
	changed := provider.ProviderSource{Github: "org/provider", Raw: "org/provider@v1.1.0"}
	assert.Equal(
		t,
		provider.GetProviderSource(original, "provider"),
		provider.GetProviderSource(changed, "provider"),
	)
	assert.Equal(t, "provider source changed", providerUpdateSkipReason(
		original, changed, "v1.0.0", "v1.2.0",
	))
	assert.Equal(t, "provider already up to date", providerUpdateSkipReason(
		original, original, "v1.3.0", "v1.2.0",
	))
}

func TestProviderUpdateSkipReason(t *testing.T) {
	const (
		originalSource = "github.com/org/provider@v1.0.0"
		updatedPin     = "github.com/org/provider@v1.1.0"
		otherSource    = "github.com/other/provider@v1.0.0"
		version100     = "v1.0.0"
		version110     = "v1.1.0"
	)
	tests := []struct {
		name           string
		originalSource provider.ProviderSource
		currentSource  provider.ProviderSource
		currentVersion string
		newVersion     string
		wantReason     string
	}{
		{
			name:           "unchanged source",
			originalSource: provider.ProviderSource{Raw: originalSource},
			currentSource:  provider.ProviderSource{Raw: originalSource},
			currentVersion: version100,
			newVersion:     version110,
		},
		{
			name:           "pin changed on same repository",
			originalSource: provider.ProviderSource{Raw: originalSource},
			currentSource:  provider.ProviderSource{Raw: updatedPin},
			currentVersion: version110,
			newVersion:     "v1.2.0",
			wantReason:     "provider source changed",
		},
		{
			name:           "repository changed",
			originalSource: provider.ProviderSource{Raw: originalSource},
			currentSource:  provider.ProviderSource{Raw: otherSource},
			currentVersion: version100,
			newVersion:     "v1.2.0",
			wantReason:     "provider source changed",
		},
		{
			name:           "current version is newer",
			originalSource: provider.ProviderSource{Raw: updatedPin},
			currentSource:  provider.ProviderSource{Raw: updatedPin},
			currentVersion: version110,
			newVersion:     version100,
			wantReason:     "provider already up to date",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantReason, providerUpdateSkipReason(
				tt.originalSource, tt.currentSource, tt.currentVersion, tt.newVersion,
			))
		})
	}
}
