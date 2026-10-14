package devcontainer

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOverlayLocalFeatureWindowsPaths(t *testing.T) {
	t.Parallel()

	id, err := rebaseOverlayLocalFeatureID(
		`C:\repo\.devcontainer\devcontainer.json`,
		`C:\repo\overlays\overlay.json`,
		"./features/tool",
	)
	require.NoError(t, err)
	require.Equal(t, "../overlays/features/tool", id)

	_, err = rebaseOverlayLocalFeatureID(
		`C:\repo\devcontainer.json`,
		`D:\overlays\overlay.json`,
		"./features/tool",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "./features/tool")
	require.Contains(t, err.Error(), strconv.Quote(`C:\repo\devcontainer.json`))
	require.Contains(t, err.Error(), strconv.Quote(`D:\overlays\overlay.json`))
}

func TestOverlayWindowsAssetPaths(t *testing.T) {
	t.Parallel()

	t.Run("absolute path is unchanged", func(t *testing.T) {
		absolute := `D:\shared\assets\Dockerfile`
		got, err := overlayAssetPath(
			absolute,
			`C:\repo\.devcontainer\devcontainer.json`,
			`D:\overlays\overlay.json`,
			"build.dockerfile",
		)
		require.NoError(t, err)
		require.Equal(t, filepath.Clean(absolute), got)
	})

	for _, field := range []string{"build.dockerfile", "build.context", "dockerComposeFile"} {
		t.Run(field+" across volumes", func(t *testing.T) {
			got, err := overlayAssetPath(
				"../assets/Dockerfile",
				`C:\repo\.devcontainer\devcontainer.json`,
				`D:\overlays\overlay.json`,
				field,
			)
			require.NoError(t, err)
			require.Equal(t, `D:\assets\Dockerfile`, got)
		})
	}
}
