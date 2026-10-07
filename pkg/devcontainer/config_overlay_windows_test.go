package devcontainer

import (
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
