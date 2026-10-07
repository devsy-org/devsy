package devcontainer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOverlayNotRequiredForComposeProjectCleanup(t *testing.T) {
	t.Parallel()
	for _, missing := range []bool{true, false} {
		name := "malformed"
		if missing {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			writeJSONConfig(t, filepath.Join(workspace, ".devcontainer.json"), map[string]any{
				"dockerComposeFile": overlayComposeFile,
				"service":           overlayService,
			})
			overlayPath := filepath.Join(workspace, "overlay.json")
			if !missing {
				require.NoError(t, os.WriteFile(overlayPath, []byte("{"), 0o600))
			}
			r := newRunnerAt(workspace)
			r.workspaceConfig.CLIOptions.ExtraDevContainerPath = overlayPath
			parsed, err := r.resolveComposeProjectConfig(context.Background())
			require.NoError(t, err)
			require.Equal(t, overlayService, parsed.Config.Service)
			require.Nil(t, parsed.Overlay)
			require.Equal(t, overlayPath, r.workspaceConfig.CLIOptions.ExtraDevContainerPath)
		})
	}
}
