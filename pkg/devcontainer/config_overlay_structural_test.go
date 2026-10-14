package devcontainer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/feature"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

const (
	structuralImage       = "overlay.test/image:latest"
	structuralService     = "overlay-service"
	structuralComposeFile = "compose.yaml"
	structuralDockerfile  = "Dockerfile"
)

func TestApplyOverlayPlanningInputsCrossFamilyTransitions(t *testing.T) {
	t.Parallel()

	families := []string{overlayImageKey, overlayDockerfileKey, overlayComposeSelection}
	for _, baseFamily := range families {
		for _, overlayFamily := range families {
			if baseFamily == overlayFamily {
				continue
			}
			t.Run(baseFamily+"_to_"+overlayFamily, func(t *testing.T) {
				root := t.TempDir()
				basePath := filepath.Join(root, overlayBaseHashKey, "devcontainer.json")
				overlayPath := filepath.Join(root, "overlay", "devcontainer.json")
				base := parseOverlayTestConfig(t, basePath, structuralFamilyConfig(baseFamily))
				overlay := parseOverlayTestConfig(
					t,
					overlayPath,
					structuralFamilyConfig(overlayFamily),
				)

				require.NoError(
					t,
					applyOverlayPlanningInputs(base, overlay, &config.SubstitutionContext{}),
				)
				require.Equal(t, basePath, base.Origin)
				switch overlayFamily {
				case overlayImageKey:
					require.Equal(t, structuralImage, base.Image)
					require.Empty(t, base.GetDockerfile())
					require.Empty(t, base.DockerComposeFile)
				case overlayDockerfileKey:
					require.Equal(
						t,
						expectedOverlayPath(basePath, overlayPath, structuralDockerfile),
						base.GetDockerfile(),
					)
					require.Empty(t, base.Image)
					require.Empty(t, base.DockerComposeFile)
				case overlayComposeSelection:
					require.Equal(
						t,
						[]string{expectedOverlayPath(basePath, overlayPath, structuralComposeFile)},
						[]string(base.DockerComposeFile),
					)
					require.Equal(t, structuralService, base.Service)
					require.Empty(t, base.Image)
					require.Empty(t, base.GetDockerfile())
				}
			})
		}
	}
}

func TestApplyOverlayPlanningInputsMergesBuildArgsAndReplacesBuildFields(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	basePath := filepath.Join(root, overlayBaseHashKey, "devcontainer.json")
	overlayPath := filepath.Join(root, "overlays", "build", "overlay.json")
	base := parseOverlayTestConfig(t, basePath, map[string]any{
		overlayBuildKey: map[string]any{
			overlayDockerfileKey: "./base.Dockerfile",
			overlayContextKey:    "./base-context",
			overlayArgsKey: map[string]string{
				overlayBaseBuildArgKey:   "kept",
				overlayBuildArgSharedKey: overlayBaseHashKey,
			},
			overlayTargetKey:    "base-target",
			overlayOptionsKey:   []string{"--no-cache"},
			overlayCacheFromKey: []string{"base-cache"},
		},
	})
	overlay := parseOverlayTestConfig(t, overlayPath, map[string]any{
		overlayBuildKey: map[string]any{
			overlayDockerfileKey: "${localEnv:OVERLAY_DOCKERFILE}",
			overlayContextKey:    "../overlay-context",
			overlayArgsKey: map[string]string{
				overlayBuildArgOverlayKey: "added",
				overlayBuildArgSharedKey:  "overlay",
			},
			overlayTargetKey:    "overlay-target",
			overlayOptionsKey:   []string{overlayPullOption},
			overlayCacheFromKey: []string{"overlay-cache"},
		},
	})
	ctx := &config.SubstitutionContext{
		Env: map[string]string{"OVERLAY_DOCKERFILE": "./overlay.Dockerfile"},
	}

	require.NoError(t, applyOverlayPlanningInputs(base, overlay, ctx))
	require.Equal(t, basePath, base.Origin)
	require.Equal(
		t,
		expectedOverlayPath(basePath, overlayPath, "./overlay.Dockerfile"),
		base.GetDockerfile(),
	)
	require.Equal(
		t,
		expectedOverlayPath(basePath, overlayPath, "../overlay-context"),
		base.GetContext(),
	)
	require.Equal(
		t,
		map[string]string{
			overlayBaseBuildArgKey:    "kept",
			overlayBuildArgOverlayKey: "added",
			overlayBuildArgSharedKey:  "overlay",
		},
		base.GetArgs(),
	)
	require.Equal(t, "overlay-target", base.GetTarget())
	require.Equal(t, []string{overlayPullOption}, []string(base.GetOptions()))
	require.Equal(t, []string{"overlay-cache"}, []string(base.GetCacheFrom()))
}

func TestApplyOverlayPlanningInputsReplacesInitializeCommand(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		overlay    any
		wantNil    bool
		wantScript []string
	}{
		{name: "replacement", overlay: "echo overlay", wantScript: []string{"echo overlay"}},
		{name: "explicit empty disables", overlay: []string{}, wantNil: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			basePath := filepath.Join(root, "base.json")
			overlayPath := filepath.Join(root, "overlay.json")
			base := parseOverlayTestConfig(
				t,
				basePath,
				map[string]any{overlayInitializeCommandKey: "echo base"},
			)
			overlay := parseOverlayTestConfig(
				t,
				overlayPath,
				map[string]any{overlayInitializeCommandKey: tc.overlay},
			)

			require.NoError(
				t,
				applyOverlayPlanningInputs(base, overlay, &config.SubstitutionContext{}),
			)
			require.Equal(t, basePath, base.Origin)
			if tc.wantNil {
				require.Nil(t, base.InitializeCommand)
			} else {
				require.Equal(t, tc.wantScript, base.InitializeCommand[""])
			}
		})
	}
}

func TestApplyOverlayPlanningInputsExplicitEmptyRunServicesClearsBase(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	basePath := filepath.Join(root, "base.json")
	overlayPath := filepath.Join(root, "overlay.json")
	base := parseOverlayTestConfig(t, basePath, map[string]any{
		overlayComposeFileKey: structuralComposeFile,
		overlayServiceKey:     structuralService,
		overlayRunServicesKey: []string{composeSecretTestServiceName, overlayDatabaseService},
	})
	overlay := parseOverlayTestConfig(
		t,
		overlayPath,
		map[string]any{overlayRunServicesKey: []string{}},
	)
	require.NotEmpty(t, overlay.Sources)

	require.NoError(t, applyOverlayPlanningInputs(base, overlay, &config.SubstitutionContext{}))
	require.NotNil(t, base.RunServices)
	require.Empty(t, base.RunServices)
}

func TestApplyOverlayPlanningInputsRejectsInvalidSelectorsAndAliasConflicts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		overlay any
	}{
		{name: "null image", overlay: map[string]any{overlayImageKey: nil}},
		{name: "empty image", overlay: map[string]any{overlayImageKey: " "}},
		{name: "empty Dockerfile alias", overlay: map[string]any{overlayLegacyDockerfileKey: ""}},
		{name: "null build", overlay: map[string]any{overlayBuildKey: nil}},
		{name: "empty Compose files", overlay: map[string]any{overlayComposeFileKey: []string{}}},
		{
			name: "conflicting selector families",
			overlay: map[string]any{
				overlayImageKey:       structuralImage,
				overlayComposeFileKey: structuralComposeFile,
				overlayServiceKey:     structuralService,
			},
		},
		{
			name: "legacy and nested Dockerfile aliases conflict",
			overlay: map[string]any{
				overlayLegacyDockerfileKey: "./legacy.Dockerfile",
				overlayBuildKey: map[string]any{
					overlayDockerfileKey: "./modern.Dockerfile",
				},
			},
		},
		{
			name: "legacy and nested context aliases conflict",
			overlay: map[string]any{
				overlayLegacyDockerfileKey: "./legacy.Dockerfile",
				overlayContextKey:          "./legacy-context",
				overlayBuildKey:            map[string]any{overlayContextKey: "./modern-context"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			basePath := filepath.Join(root, "base.json")
			overlayPath := filepath.Join(root, "overlay.json")
			base := parseOverlayTestConfig(
				t,
				basePath,
				map[string]any{overlayImageKey: overlayBaseImage},
			)
			overlay := parseOverlayTestConfig(t, overlayPath, tc.overlay)

			require.Error(
				t,
				applyOverlayPlanningInputs(base, overlay, &config.SubstitutionContext{}),
			)
		})
	}
}

func TestGetSubstitutedConfigCLIImageOverridesStructuralOverlay(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	basePath := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	overlayPath := filepath.Join(workspace, "overlay.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0o750))
	writeJSONConfig(t, basePath, map[string]any{overlayImageKey: overlayBaseImage})
	writeJSONConfig(t, overlayPath, structuralFamilyConfig(overlayDockerfileKey))

	parsed, _, err := newRunnerAt(workspace).getSubstitutedConfig(provider.CLIOptions{
		ExtraDevContainerPath: overlayPath,
		DevContainerImage:     "cli-image",
	})
	require.NoError(t, err)
	require.Equal(t, "cli-image", parsed.Config.Image)
	require.Empty(t, parsed.Config.GetDockerfile())
	require.Empty(t, parsed.Config.DockerComposeFile)
	require.Equal(t, basePath, parsed.Config.Origin)
}

func TestApplyOverlayPlanningInputsKeepsInheritedOverlayPathProvenance(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	basePath := filepath.Join(root, overlayBaseHashKey, "devcontainer.json")
	overlayParentPath := filepath.Join(root, "overlays", "shared", "parent.json")
	overlayPath := filepath.Join(root, "overlays", "project", "overlay.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Dir(overlayPath), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Dir(overlayParentPath), 0o750))
	writeJSONConfig(t, basePath, map[string]any{overlayImageKey: overlayBaseImage})
	writeJSONConfig(t, overlayParentPath, map[string]any{
		overlayBuildKey: map[string]any{
			overlayDockerfileKey: "../shared/" + structuralDockerfile,
			overlayContextKey:    "../shared/context",
		},
	})
	writeJSONConfig(t, overlayPath, map[string]any{
		overlayExtendsKey: "../shared/parent.json",
		overlayBuildKey: map[string]any{
			overlayArgsKey: map[string]string{overlayBuildArgOverlayKey: "child"},
		},
	})

	base := parseOverlayTestConfig(t, basePath, nil)
	overlay := parseOverlayTestConfig(t, overlayPath, nil)
	require.Len(t, overlay.Sources, 2)
	require.NoError(t, applyOverlayPlanningInputs(base, overlay, &config.SubstitutionContext{}))
	require.Equal(t, basePath, base.Origin)
	require.Equal(t, feature.LockfilePath(basePath), feature.LockfilePath(base.Origin))
	require.Equal(
		t,
		expectedOverlayPath(basePath, overlayParentPath, "../shared/"+structuralDockerfile),
		base.GetDockerfile(),
	)
	require.Equal(
		t,
		expectedOverlayPath(basePath, overlayParentPath, "../shared/context"),
		base.GetContext(),
	)
	require.Equal(t, map[string]string{overlayBuildArgOverlayKey: "child"}, base.GetArgs())
}

func parseOverlayTestConfig(t *testing.T, path string, value any) *config.DevContainerConfig {
	t.Helper()
	if value != nil {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		writeJSONConfig(t, path, value)
	}
	parsed, err := config.ParseDevContainerJSONFile(context.Background(), path)
	require.NoError(t, err)
	require.NotEmpty(t, parsed.Sources)
	return parsed
}

func structuralFamilyConfig(family string) map[string]any {
	switch family {
	case overlayImageKey:
		return map[string]any{overlayImageKey: structuralImage}
	case overlayDockerfileKey:
		return map[string]any{
			overlayBuildKey: map[string]any{
				overlayDockerfileKey: structuralDockerfile,
				overlayContextKey:    "./context",
			},
		}
	case overlayComposeSelection:
		return map[string]any{
			overlayComposeFileKey: "./" + structuralComposeFile,
			overlayServiceKey:     structuralService,
			overlayRunServicesKey: []string{overlayDatabaseService},
		}
	default:
		panic("unknown structural fixture family: " + family)
	}
}

func expectedOverlayPath(basePath, sourcePath, sourceValue string) string {
	resolved := filepath.Join(filepath.Dir(sourcePath), filepath.FromSlash(sourceValue))
	relative, err := filepath.Rel(filepath.Dir(basePath), resolved)
	if err != nil {
		panic(err)
	}
	return filepath.ToSlash(relative)
}

func TestOverlayNullFeaturePlanningFieldsKeepPrimaryValues(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	base := parseOverlayTestConfig(t, filepath.Join(workspace, "base.json"), map[string]any{
		overlayImageField:                       structuralImage,
		overlayFeaturesField:                    map[string]any{overlayNodeID: map[string]any{}},
		overlayOverrideFeatureInstallOrderField: []string{overlayNodeID},
	})
	overlay := parseOverlayTestConfig(t, filepath.Join(workspace, "overlay.json"), map[string]any{
		overlayFeaturesField:                    nil,
		overlayOverrideFeatureInstallOrderField: nil,
	})
	require.NoError(t, applyOverlayPlanningInputs(base, overlay, &config.SubstitutionContext{}))
	require.Contains(t, base.Features, overlayNodeID)
	require.Equal(t, []string{overlayNodeID}, base.OverrideFeatureInstallOrder)
}
