package devcontainer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/feature"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

const (
	overlayImageKey       = string(SourceImage)
	overlayVersionKey     = "version"
	overlayComposeFile    = "compose.yaml"
	overlayService        = "overlay-test"
	overlayNodeID         = "example/node"
	overlayBaseID         = "example/base"
	overlayExtraID        = "example/overlay"
	overlayToolID         = "example/tool"
	overlayBaseFile       = "/workspace/.devcontainer/devcontainer.json"
	overlayImage          = "alpine:latest"
	overlayDockerfile     = "Dockerfile"
	overlayDockerfileKind = "Dockerfile config"
	overlayBaseHashKey    = "base"
	overlayFeaturesKey    = "features"
	overlayFeatureV1Case  = "feature option v1"
	overlayFeatureV2Case  = "feature option v2"
)

func marshalConfigValue(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

func writeJSONConfig(t *testing.T, path string, value any) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(marshalConfigValue(t, value)), 0o600))
}

func TestApplyOverlayBuildInputs(t *testing.T) {
	t.Parallel()

	baseOrigin := filepath.Join(t.TempDir(), "base", "devcontainer.json")
	overlayOrigin := filepath.Join(t.TempDir(), "overlay", "devcontainer.json")
	base := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			Features: map[string]any{
				overlayNodeID: map[string]any{overlayVersionKey: "18", "baseOnlyOption": true},
				overlayBaseID: map[string]any{},
			},
			OverrideFeatureInstallOrder: []string{overlayBaseID, overlayNodeID},
		},
		Origin: baseOrigin,
	}
	overlay := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			Features: map[string]any{
				overlayNodeID:  map[string]any{overlayVersionKey: "22"},
				overlayExtraID: map[string]any{},
			},
			OverrideFeatureInstallOrder: []string{overlayExtraID, overlayNodeID},
		},
		Origin: overlayOrigin,
	}

	require.NoError(t, applyOverlayBuildInputs(base, overlay))
	require.Equal(t, baseOrigin, base.Origin)
	require.Equal(t, map[string]any{overlayVersionKey: "22"}, base.Features[overlayNodeID])
	require.Contains(t, base.Features, overlayBaseID)
	require.Contains(t, base.Features, overlayExtraID)
	require.Equal(t, []string{overlayExtraID, overlayNodeID}, base.OverrideFeatureInstallOrder)
	// Applying the overlay must not rewrite the parsed overlay source object.
	require.Equal(t, map[string]any{overlayVersionKey: "22"}, overlay.Features[overlayNodeID])
}

func TestApplyOverlayBuildInputsRebasesLocalFeatureIDs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	baseOrigin := filepath.Join(root, "base", ".devcontainer", "devcontainer.json")
	overlayOrigin := filepath.Join(root, "overlays", "nested", "overlay.json")
	localFeatureID := filepath.ToSlash(
		filepath.Join("..", "..", "overlays", "nested", "features", "local"),
	)
	base := &config.DevContainerConfig{
		Origin: baseOrigin,
		DevContainerConfigBase: config.DevContainerConfigBase{
			Features: map[string]any{localFeatureID: map[string]any{"baseOnlyOption": true}},
		},
	}
	overlay := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			Features: map[string]any{
				"./features/local": map[string]any{},
				"../shared/tool":   map[string]any{},
			},
			OverrideFeatureInstallOrder: []string{"../shared/tool", "./features/local"},
		},
		Origin: overlayOrigin,
	}

	require.NoError(t, applyOverlayBuildInputs(base, overlay))
	parentFeatureID := filepath.ToSlash(filepath.Join("..", "..", "overlays", "shared", "tool"))
	require.Equal(t, map[string]any{}, base.Features[localFeatureID])
	require.Contains(t, base.Features, parentFeatureID)
	require.Equal(t, []string{parentFeatureID, localFeatureID}, base.OverrideFeatureInstallOrder)
	require.Equal(t, baseOrigin, base.Origin)
	require.Contains(t, overlay.Features, "./features/local")
}

func TestApplyOverlayBuildInputsNilAndEmptyOverlay(t *testing.T) {
	t.Parallel()

	base := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			Features:                    map[string]any{overlayBaseID: map[string]any{}},
			OverrideFeatureInstallOrder: []string{overlayBaseID},
		},
		Origin: overlayBaseFile,
	}
	original := config.CloneDevContainerConfig(base)
	require.NoError(t, applyOverlayBuildInputs(base, nil))
	require.Equal(t, original, base)

	empty := &config.DevContainerConfig{Origin: "/overlay.json"}
	require.NoError(t, applyOverlayBuildInputs(base, empty))
	require.Equal(t, original, base)
}

func TestApplyOverlayBuildInputsPreservesBaseOrderWhenOverlayOmitsOrder(t *testing.T) {
	t.Parallel()

	base := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			OverrideFeatureInstallOrder: []string{"example/second", "example/first"},
		},
		Origin: overlayBaseFile,
	}
	overlay := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			Features: map[string]any{overlayExtraID: map[string]any{}},
		},
		Origin: "/workspace/overlay.json",
	}

	require.NoError(t, applyOverlayBuildInputs(base, overlay))
	require.Equal(t, []string{"example/second", "example/first"}, base.OverrideFeatureInstallOrder)
}

func TestApplyOverlayBuildInputsReplacesOrderWithExplicitEmptyOrder(t *testing.T) {
	t.Parallel()

	base := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			OverrideFeatureInstallOrder: []string{"example/feature"},
		},
		Origin: overlayBaseFile,
	}
	overlay := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			OverrideFeatureInstallOrder: []string{},
		},
		Origin: "/workspace/overlay.json",
	}

	require.NoError(t, applyOverlayBuildInputs(base, overlay))
	require.NotNil(t, base.OverrideFeatureInstallOrder)
	require.Empty(t, base.OverrideFeatureInstallOrder)
}

func TestGetSubstitutedConfigWithOverlayPrecedenceAndSubstitution(t *testing.T) {
	workspace := t.TempDir()
	basePath := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	overlayPath := filepath.Join(workspace, "overlays", "overlay.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Dir(overlayPath), 0o750))
	writeJSONConfig(t, basePath, map[string]any{
		overlayImageKey: overlayImage,
		overlayFeaturesKey: map[string]any{
			overlayNodeID: map[string]any{overlayVersionKey: "18"},
			overlayBaseID: map[string]any{},
		},
	})
	writeJSONConfig(t, overlayPath, map[string]any{
		overlayFeaturesKey: map[string]any{
			overlayNodeID:  map[string]any{overlayVersionKey: "${localEnv:OVERLAY_VERSION}"},
			overlayExtraID: map[string]any{overlayVersionKey: "${localEnv:OVERLAY_VERSION}"},
		},
	})
	t.Setenv("OVERLAY_VERSION", "22")

	r := newRunnerAt(workspace)
	rawParsed, substitutionContext, err := r.getSubstitutedConfig(provider2.CLIOptions{
		ExtraDevContainerPath: overlayPath,
		AdditionalFeatures: marshalConfigValue(t, map[string]any{
			overlayNodeID: map[string]any{overlayVersionKey: "23"},
		}),
	})
	require.NoError(t, err)
	require.NotNil(t, substitutionContext)
	require.Equal(t, basePath, rawParsed.Config.Origin)
	require.Equal(
		t,
		"${localEnv:OVERLAY_VERSION}",
		rawParsed.Raw.Features[overlayNodeID].(map[string]any)[overlayVersionKey],
	)
	require.Equal(
		t,
		"23",
		rawParsed.Config.Features[overlayNodeID].(map[string]any)[overlayVersionKey],
	)
	require.Equal(
		t,
		"${localEnv:OVERLAY_VERSION}",
		rawParsed.Raw.Features[overlayExtraID].(map[string]any)[overlayVersionKey],
	)
	require.Equal(
		t,
		"22",
		rawParsed.Config.Features[overlayExtraID].(map[string]any)[overlayVersionKey],
	)
	require.Contains(t, rawParsed.Config.Features, overlayBaseID)
	require.Contains(t, rawParsed.Config.Features, overlayExtraID)
}

func TestGetSubstitutedConfigRejectsMissingOrMalformedOverlay(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	seedConfigAt(t, workspace, filepath.Join(".devcontainer", "devcontainer.json"))
	r := newRunnerAt(workspace)

	_, _, err := r.getSubstitutedConfig(provider2.CLIOptions{
		ExtraDevContainerPath: filepath.Join(workspace, "missing.json"),
	})
	require.Error(t, err)

	overlayPath := filepath.Join(workspace, "malformed.json")
	require.NoError(t, os.WriteFile(overlayPath, []byte("{"), 0o600))
	_, _, err = r.getSubstitutedConfig(provider2.CLIOptions{ExtraDevContainerPath: overlayPath})
	require.Error(t, err)
	require.Contains(t, err.Error(), overlayPath)
}

func TestMergeImageMetadataConfigReusesParsedOverlaySnapshot(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	basePath := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	overlayPath := filepath.Join(workspace, "overlay.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0o750))
	writeJSONConfig(t, basePath, map[string]any{overlayImageKey: overlayImage})
	require.NoError(
		t,
		os.WriteFile(overlayPath, []byte(`{"remoteEnv":{"FROM_OVERLAY":"snapshot"}}`), 0o600),
	)

	parsed, _, err := newRunnerAt(workspace).getSubstitutedConfig(provider2.CLIOptions{
		ExtraDevContainerPath: overlayPath,
	})
	require.NoError(t, err)
	require.NotNil(t, parsed.Overlay)
	require.Empty(t, parsed.Raw.RemoteEnv)
	require.Empty(t, parsed.Config.RemoteEnv)
	require.Equal(t, overlayPath, parsed.Overlay.Origin)
	require.NoError(t, os.WriteFile(overlayPath, []byte("{"), 0o600))

	merged, err := mergeImageMetadataConfig(context.Background(), parsed, nil, overlayPath)
	require.NoError(t, err)
	require.NotNil(t, merged.RemoteEnv["FROM_OVERLAY"])
	require.Equal(t, "snapshot", *merged.RemoteEnv["FROM_OVERLAY"])
}

func TestGetSubstitutedConfigOverlayAppliesAcrossConfigKinds(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		config any
	}{
		{name: overlayImageKey, config: map[string]any{overlayImageKey: overlayImage}},
		{name: overlayDockerfileKind, config: map[string]any{"build": map[string]any{"dockerfile": overlayDockerfile}}},
		{name: "Compose", config: map[string]any{"dockerComposeFile": overlayComposeFile, "service": overlayService}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			basePath := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
			overlayPath := filepath.Join(workspace, "overlay.json")
			require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0o750))
			writeJSONConfig(t, basePath, tc.config)
			writeJSONConfig(t, overlayPath, map[string]any{
				overlayFeaturesKey: map[string]any{overlayExtraID: map[string]any{}},
			})

			parsed, _, err := newRunnerAt(workspace).getSubstitutedConfig(provider2.CLIOptions{
				ExtraDevContainerPath: overlayPath,
			})
			require.NoError(t, err)
			require.Contains(t, parsed.Config.Features, overlayExtraID)
		})
	}
}

func TestOverlayFeaturesAndOrderAffectPrebuildHash(t *testing.T) {
	t.Parallel()

	contextPath := t.TempDir()
	base := &config.DevContainerConfig{Origin: filepath.Join(contextPath, "devcontainer.json")}
	baseHash := overlayPrebuildHash(t, contextPath, base)

	featureConfig := &config.DevContainerConfig{
		Origin: filepath.Join(contextPath, "overlay.json"),
		DevContainerConfigBase: config.DevContainerConfigBase{
			Features: map[string]any{overlayToolID: map[string]any{overlayVersionKey: "1"}},
		},
	}
	withFeature := config.CloneDevContainerConfig(base)
	require.NoError(t, applyOverlayBuildInputs(withFeature, featureConfig))
	featureHash := overlayPrebuildHash(t, contextPath, withFeature)
	require.NotEqual(t, baseHash, featureHash)

	tests := []struct {
		name        string
		overlay     *config.DevContainerConfig
		sameAs      bool
		compareHash string
	}{
		{
			name:        overlayFeatureV2Case,
			overlay:     testFeatureOverlay(contextPath, "2", nil),
			compareHash: featureHash,
		},
		{
			name:        "install order",
			overlay:     testFeatureOverlay(contextPath, "1", []string{overlayToolID}),
			compareHash: featureHash,
		},
		{
			name: "runtime metadata",
			overlay: &config.DevContainerConfig{
				Origin: filepath.Join(contextPath, "overlay.json"),
				DevContainerConfigBase: config.DevContainerConfigBase{
					RemoteEnv: map[string]*string{"KEY": new("value")},
				},
			},
			sameAs:      true,
			compareHash: baseHash,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate := config.CloneDevContainerConfig(base)
			require.NoError(t, applyOverlayBuildInputs(candidate, tc.overlay))
			hash := overlayPrebuildHash(t, contextPath, candidate)
			if tc.sameAs {
				require.Equal(t, tc.compareHash, hash)
			} else {
				require.NotEqual(t, tc.compareHash, hash)
			}
		})
	}
}

func testFeatureOverlay(contextPath, version string, order []string) *config.DevContainerConfig {
	return &config.DevContainerConfig{
		Origin: filepath.Join(contextPath, "overlay.json"),
		DevContainerConfigBase: config.DevContainerConfigBase{
			Features: map[string]any{
				overlayToolID: map[string]any{overlayVersionKey: version},
			},
			OverrideFeatureInstallOrder: order,
		},
	}
}

func overlayPrebuildHash(t *testing.T, contextPath string, cfg *config.DevContainerConfig) string {
	t.Helper()
	hash, err := config.CalculatePrebuildHash(config.PrebuildHashParams{
		Config:            cfg,
		Architecture:      "amd64",
		ContextPath:       contextPath,
		DockerfilePath:    overlayDockerfile,
		DockerfileContent: "FROM " + overlayImage + "\n",
	})
	require.NoError(t, err)
	return hash
}

func TestOverlayFeatureUsesPrimaryLockfileOrigin(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	basePath := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	overlayPath := filepath.Join(workspace, "overlay.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0o750))
	writeJSONConfig(t, basePath, map[string]any{overlayImageKey: overlayImage})
	require.NoError(
		t,
		os.WriteFile(overlayPath, []byte(`{"features":{"ghcr.io/example/overlay:1":{}}}`), 0o600),
	)

	parsed, substitutionContext, err := newRunnerAt(
		workspace,
	).getSubstitutedConfig(provider2.CLIOptions{
		ExtraDevContainerPath: overlayPath,
	})
	require.NoError(t, err)
	require.Contains(t, parsed.Config.Features, "ghcr.io/example/overlay:1")

	_, err = feature.GetExtendedBuildInfo(&feature.ExtendedBuildParams{
		ExecutionContext:   context.Background(),
		Ctx:                substitutionContext,
		ImageBuildInfo:     &config.ImageBuildInfo{},
		DevContainerConfig: parsed,
		FrozenLockfile:     true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "lockfile")
	require.Equal(t, basePath, parsed.Config.Origin)
	require.Equal(
		t,
		filepath.Join(filepath.Dir(basePath), "devcontainer-lock.json"),
		feature.LockfilePath(parsed.Config.Origin),
	)
	require.NoFileExists(t, feature.LockfilePath(basePath))
	require.NoFileExists(t, feature.LockfilePath(overlayPath))
}

func TestOverlayLocalFeaturesReachBuildPlanning(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	basePath := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	overlayPath := filepath.Join(workspace, "overlays", "overlay.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0o750))
	writeJSONConfig(t, basePath, map[string]any{overlayImageKey: overlayImage})
	for _, id := range []string{"alpha", "bravo"} {
		dir := filepath.Join(filepath.Dir(overlayPath), "features", id)
		require.NoError(t, os.MkdirAll(dir, 0o750))
		data, err := json.Marshal(
			map[string]string{"id": id, overlayVersionKey: "1.0.0", "name": id},
		)
		require.NoError(t, err)
		require.NoError(
			t,
			os.WriteFile(filepath.Join(dir, "devcontainer-feature.json"), data, 0o600),
		)
		require.NoError(
			t,
			os.WriteFile(filepath.Join(dir, "install.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o600),
		)
	}
	writeJSONConfig(t, overlayPath, map[string]any{
		overlayFeaturesKey: map[string]any{
			"./features/alpha": map[string]any{},
			"./features/bravo": map[string]any{},
		},
		"overrideFeatureInstallOrder": []string{"./features/bravo", "./features/alpha"},
	})
	parsed, substitutionContext, err := newRunnerAt(
		workspace,
	).getSubstitutedConfig(provider2.CLIOptions{ExtraDevContainerPath: overlayPath})
	require.NoError(t, err)
	info, err := feature.GetExtendedBuildInfo(&feature.ExtendedBuildParams{
		ExecutionContext: context.Background(), Ctx: substitutionContext,
		ImageBuildInfo:     &config.ImageBuildInfo{Metadata: &config.ImageMetadataConfig{}},
		DevContainerConfig: parsed,
	})
	require.NoError(t, err)
	require.NotNil(t, info.FeaturesBuildInfo)
	require.Len(t, info.Features, 2)
	require.Equal(t, "bravo", info.Features[0].Config.ID)
	require.Equal(t, "alpha", info.Features[1].Config.ID)
	require.Equal(
		t,
		filepath.Join(filepath.Dir(overlayPath), "features", "bravo"),
		info.Features[0].Folder,
	)
	require.NoFileExists(t, feature.LockfilePath(basePath))
	require.NoFileExists(t, feature.LockfilePath(overlayPath))
}

func TestOverlayLocalFeatureIDsKeepLocalPrefix(t *testing.T) {
	t.Parallel()
	origin := filepath.Join(t.TempDir(), "devcontainer.json")
	for _, id := range []string{"./features/tool", "./", "../features/tool"} {
		t.Run(id, func(t *testing.T) {
			rebased, err := rebaseOverlayLocalFeatureID(origin, origin, id)
			require.NoError(t, err)
			require.Equal(t, id, rebased)
		})
	}
}
