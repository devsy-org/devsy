package devcontainer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

type overlayNullValueCase struct {
	name          string
	overlay       map[string]any
	errorContains string
}

var overlayNullValueCases = []overlayNullValueCase{
	{
		name: "build args entry",
		overlay: map[string]any{
			overlayBuildKey: map[string]any{
				overlayArgsKey: map[string]any{"INVALID": nil},
			},
		},
		errorContains: "build.args.INVALID",
	},
	{
		name: "cacheFrom entry",
		overlay: map[string]any{
			overlayBuildKey: map[string]any{
				overlayCacheFromKey: []any{"cache:latest", nil},
			},
		},
		errorContains: "unsupported type",
	},
	{
		name: "build option entry",
		overlay: map[string]any{
			overlayBuildKey: map[string]any{
				overlayOptionsKey: []any{nil},
			},
		},
		errorContains: "build.options[0]",
	},
	{
		name: "runServices entry",
		overlay: map[string]any{
			overlayRunServicesKey: []any{nil},
		},
		errorContains: "runServices[0]",
	},
}

func TestResolvedOverlayRejectsNullStructuralValues(t *testing.T) {
	t.Parallel()
	for _, tc := range overlayNullValueCases {
		t.Run(tc.name, func(t *testing.T) {
			assertResolvedOverlayNullValueError(t, tc)
		})
	}
}

func assertResolvedOverlayNullValueError(t *testing.T, tc overlayNullValueCase) {
	t.Helper()
	workspace := t.TempDir()
	basePath := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	overlayPath := filepath.Join(workspace, "overlay.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0o750))
	writeJSONConfig(t, basePath, dockerfileNullTestConfig())
	writeJSONConfig(t, overlayPath, tc.overlay)

	_, _, err := newRunnerAt(workspace).getSubstitutedConfig(provider.CLIOptions{
		ExtraDevContainerPath: overlayPath,
	})
	require.Error(t, err)
	require.ErrorContains(t, err, tc.errorContains)
}

func dockerfileNullTestConfig() map[string]any {
	return map[string]any{
		overlayBuildKey: map[string]any{
			overlayDockerfileKey: structuralDockerfile,
			overlayContextKey:    ".",
		},
	}
}

func TestResolvedOverlayAllowsEmptyBuildValues(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	basePath := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	overlayPath := filepath.Join(workspace, "overlay.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0o750))
	writeJSONConfig(t, basePath, dockerfileNullTestConfig())
	writeJSONConfig(t, overlayPath, map[string]any{
		overlayBuildKey: map[string]any{
			overlayArgsKey:      map[string]string{},
			overlayCacheFromKey: []string{},
			overlayOptionsKey:   []string{},
		},
	})

	parsed, _, err := newRunnerAt(workspace).getSubstitutedConfig(provider.CLIOptions{
		ExtraDevContainerPath: overlayPath,
	})
	require.NoError(t, err)
	require.Empty(t, parsed.Config.GetArgs())
	require.Empty(t, parsed.Config.GetCacheFrom())
	require.Empty(t, parsed.Config.GetOptions())
}
