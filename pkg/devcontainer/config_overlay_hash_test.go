package devcontainer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

const (
	overlayBaseBuildArgKey = "BASE"
	overlayPullOption      = "--pull"
	overlayDatabaseService = "database"
)

type overlayPrebuildHashCase struct {
	name        string
	base        map[string]any
	overlay     map[string]any
	wantChanged bool
}

var overlayPrebuildHashCases = []overlayPrebuildHashCase{
	{
		name: structuralDockerfile,
		base: dockerfileHashBase(),
		overlay: map[string]any{
			overlayBuildKey: map[string]any{overlayDockerfileKey: "./Overlay.Dockerfile"},
		},
		wantChanged: true,
	},
	{
		name: "context",
		base: dockerfileHashBase(),
		overlay: map[string]any{
			overlayBuildKey: map[string]any{overlayContextKey: "./alternate-context"},
		},
		wantChanged: true,
	},
	{
		name: "build args",
		base: dockerfileHashBase(),
		overlay: map[string]any{
			overlayBuildKey: map[string]any{
				overlayArgsKey: map[string]string{"OVERLAY": "changed"},
			},
		},
		wantChanged: true,
	},
	{
		name: "target",
		base: dockerfileHashBase(),
		overlay: map[string]any{
			overlayBuildKey: map[string]any{overlayTargetKey: "release"},
		},
		wantChanged: true,
	},
	{
		name: "cacheFrom",
		base: dockerfileHashBase(),
		overlay: map[string]any{
			overlayBuildKey: map[string]any{overlayCacheFromKey: []string{"cache:latest"}},
		},
		wantChanged: true,
	},
	{
		name: "options",
		base: dockerfileHashBase(),
		overlay: map[string]any{
			overlayBuildKey: map[string]any{overlayOptionsKey: []string{overlayPullOption}},
		},
		wantChanged: true,
	},
	{
		name:        "image selector and image",
		base:        dockerfileHashBase(),
		overlay:     map[string]any{overlayImageKey: "alpine:3.21"},
		wantChanged: true,
	},
	{
		name: "Compose selector",
		base: dockerfileHashBase(),
		overlay: map[string]any{
			overlayComposeFileKey: overlayComposeFile,
			overlayServiceKey:     overlayService,
		},
		wantChanged: true,
	},
	{
		name: "Compose file",
		base: composeHashBase(),
		overlay: map[string]any{
			overlayComposeFileKey: "alternate-compose.yaml",
		},
	},
	{
		name:    "Compose service",
		base:    composeHashBase(),
		overlay: map[string]any{overlayServiceKey: "alternate-service"},
	},
	{
		name:    "runServices",
		base:    composeHashBase(),
		overlay: map[string]any{overlayRunServicesKey: []string{overlayDatabaseService}},
	},
}

func dockerfileHashBase() map[string]any {
	return map[string]any{
		overlayBuildKey: map[string]any{
			overlayDockerfileKey: structuralDockerfile,
			overlayContextKey:    ".",
			overlayArgsKey:       map[string]string{overlayBaseBuildArgKey: overlayBaseHashKey},
		},
	}
}

func composeHashBase() map[string]any {
	return map[string]any{
		overlayComposeFileKey: overlayComposeFile,
		overlayServiceKey:     overlayService,
		overlayRunServicesKey: []string{composeSecretTestServiceName},
	}
}

func TestResolvedOverlayBuildInputsAffectPrebuildHash(t *testing.T) {
	t.Parallel()
	for _, tc := range overlayPrebuildHashCases {
		t.Run(tc.name, func(t *testing.T) {
			assertOverlayPrebuildHashCase(t, tc)
		})
	}
}

func assertOverlayPrebuildHashCase(t *testing.T, tc overlayPrebuildHashCase) {
	t.Helper()
	workspace := t.TempDir()
	basePath := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0o750))
	writeJSONConfig(t, basePath, tc.base)
	hashContext := t.TempDir()
	baselineHash := overlayPrebuildHash(
		t,
		hashContext,
		resolveOverlayHashConfig(t, workspace, ""),
	)
	overlayPath := filepath.Join(workspace, "overlay.json")
	writeJSONConfig(t, overlayPath, tc.overlay)
	overlayHash := overlayPrebuildHash(
		t,
		hashContext,
		resolveOverlayHashConfig(t, workspace, overlayPath),
	)
	if tc.wantChanged {
		require.NotEqual(t, baselineHash, overlayHash)
	} else {
		require.Equal(t, baselineHash, overlayHash)
	}
}

func resolveOverlayHashConfig(
	t *testing.T,
	workspace, overlayPath string,
) *config.DevContainerConfig {
	t.Helper()
	options := provider.CLIOptions{}
	if overlayPath != "" {
		options.ExtraDevContainerPath = overlayPath
	}
	parsed, _, err := newRunnerAt(
		workspace,
	).getSubstitutedConfigWithContext(context.Background(), options)
	require.NoError(t, err)
	if overlayPath != "" {
		require.NotNil(t, parsed.Overlay)
	}
	return parsed.Config
}

func TestOverlaySourceProvenanceIsExcludedFromJSONAndPrebuildHash(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	basePath := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	overlayPath := filepath.Join(workspace, "overlay.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0o750))
	writeJSONConfig(t, basePath, map[string]any{overlayImageKey: overlayImage})
	writeJSONConfig(
		t,
		overlayPath,
		map[string]any{overlayFeaturesKey: map[string]any{overlayToolID: map[string]any{}}},
	)
	parsed, _, err := newRunnerAt(workspace).getSubstitutedConfig(provider.CLIOptions{
		ExtraDevContainerPath: overlayPath,
	})
	require.NoError(t, err)
	require.NotEmpty(t, parsed.Raw.Sources)
	require.NotEmpty(t, parsed.Overlay.Sources)

	withDifferentProvenance := config.CloneDevContainerConfig(parsed.Config)
	withDifferentProvenance.Origin = filepath.Join(t.TempDir(), "different-primary.json")
	withDifferentProvenance.Sources = append(
		append([]config.ConfigSource(nil), parsed.Raw.Sources...),
		parsed.Overlay.Sources...,
	)
	for i := range withDifferentProvenance.Sources {
		withDifferentProvenance.Sources[i].Origin = filepath.Join(
			t.TempDir(),
			"different-source.json",
		)
		withDifferentProvenance.Sources[i].Fields = map[string]json.RawMessage{
			overlayImageKey: json.RawMessage(`"unrelated"`),
		}
	}

	serialized, err := json.Marshal(parsed.Config)
	require.NoError(t, err)
	otherSerialized, err := json.Marshal(withDifferentProvenance)
	require.NoError(t, err)
	require.JSONEq(t, string(serialized), string(otherSerialized))
	require.NotContains(t, string(serialized), "Sources")
	require.NotContains(t, string(serialized), overlayPath)
	require.NotContains(t, string(serialized), basePath)

	hashContext := t.TempDir()
	require.Equal(
		t,
		overlayPrebuildHash(t, hashContext, parsed.Config),
		overlayPrebuildHash(t, hashContext, withDifferentProvenance),
	)
}
