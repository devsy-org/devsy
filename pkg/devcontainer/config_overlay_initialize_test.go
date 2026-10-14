package devcontainer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

const overlayInitValueEnv = "OVERLAY_INIT_VALUE"

func TestOverlayInitializeCommandFormsResolveAndRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command any
	}{
		{
			name: "string command",
			command: fmt.Sprintf(
				`printf '%%s|%%s' "$PWD" "${localEnv:%s}" > "${localEnv:OVERLAY_INIT_OUTPUT}"`,
				overlayInitValueEnv,
			),
		},
		{
			name: "argv command",
			command: []string{
				"overlay-init-helper",
				"-test.run=TestOverlayInitializeHelperProcess",
				"${localEnv:" + overlayInitValueEnv + "}",
			},
		},
		{
			name: "named command",
			command: map[string]any{
				"write marker": []string{
					"overlay-init-helper",
					"-test.run=TestOverlayInitializeHelperProcess",
					"${localEnv:" + overlayInitValueEnv + "}",
				},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertOverlayInitializeCommandForm(t, tc.command)
		})
	}
}

func assertOverlayInitializeCommandForm(t *testing.T, command any) {
	t.Helper()
	workspace := t.TempDir()
	basePath := filepath.Join(workspace, "base.json")
	overlayPath := filepath.Join(workspace, "overlay.json")
	outputPath := filepath.Join(workspace, "initialize.out")
	writeJSONConfig(t, basePath, map[string]any{overlayImageKey: overlayBaseImage})
	command = replaceOverlayInitExecutable(t, command)
	writeJSONConfig(t, overlayPath, map[string]any{overlayInitializeCommandKey: command})

	initEnv := []string{
		overlayInitValueEnv + "=resolved-from-init-env",
		"OVERLAY_INIT_OUTPUT=" + outputPath,
		"OVERLAY_INIT_HELPER=1",
	}
	parsed, _, err := newRunnerAt(workspace).getSubstitutedConfig(provider.CLIOptions{
		DevContainerPath:      filepath.Base(basePath),
		ExtraDevContainerPath: overlayPath,
		InitEnv:               initEnv,
	})
	require.NoError(t, err)
	require.NotNil(t, parsed.Overlay)
	require.NotEmpty(t, parsed.Config.InitializeCommand)
	require.NoError(
		t,
		runInitializeCommand(context.Background(), workspace, parsed.Config, initEnv),
	)

	// #nosec G304 -- outputPath is under this test's temporary workspace.
	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assertOverlayInitializeOutput(t, workspace, data)
}

func assertOverlayInitializeOutput(t *testing.T, workspace string, data []byte) {
	t.Helper()
	parts := strings.SplitN(string(data), "|", 3)
	require.Len(t, parts, 3)
	wantWorkspace, err := filepath.EvalSymlinks(workspace)
	require.NoError(t, err)
	actualWorkspace, err := filepath.EvalSymlinks(parts[0])
	require.NoError(t, err)
	require.Equal(t, wantWorkspace, actualWorkspace)
	require.Equal(t, "resolved-from-init-env", parts[1])
	require.Equal(t, "resolved-from-init-env", parts[2])
}

func replaceOverlayInitExecutable(t *testing.T, command any) any {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	switch value := command.(type) {
	case string:
		return fmt.Sprintf(
			`"%s" -test.run=TestOverlayInitializeHelperProcess "${localEnv:%s}"`,
			executable,
			overlayInitValueEnv,
		)
	case []string:
		value[0] = executable
		return value
	case map[string]any:
		for name, raw := range value {
			argv := raw.([]string)
			argv[0] = executable
			value[name] = argv
		}
		return value
	default:
		return command
	}
}

func TestOverlayInitializeCommandFailureAndCancellationPreventMarker(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("failure command uses sh syntax")
	}

	for _, canceled := range []bool{false, true} {
		name := "command failure"
		if canceled {
			name = "canceled context"
		}
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			marker := filepath.Join(workspace, "must-not-exist")
			command := "exit 1; printf unexpected > " + marker
			if canceled {
				command = "printf unexpected > " + marker
			}
			conf := parseOverlayTestConfig(
				t,
				filepath.Join(workspace, "overlay.json"),
				map[string]any{
					overlayInitializeCommandKey: command,
				},
			)
			ctx := context.Background()
			if canceled {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			require.Error(t, runInitializeCommand(ctx, workspace, conf, nil))
			_, err := os.Stat(marker)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestOverlayInitializeHelperProcess(t *testing.T) {
	if os.Getenv("OVERLAY_INIT_HELPER") != "1" {
		return
	}
	outputPath := os.Getenv("OVERLAY_INIT_OUTPUT")
	workingDirectory, err := os.Getwd()
	require.NoError(t, err)
	absoluteWorkingDirectory, err := filepath.Abs(workingDirectory)
	require.NoError(t, err)
	absoluteOutputPath, err := filepath.Abs(outputPath)
	require.NoError(t, err)
	relativeOutputPath, err := filepath.Rel(absoluteWorkingDirectory, absoluteOutputPath)
	require.NoError(t, err)
	require.NotEqual(t, "..", relativeOutputPath)
	require.False(t, strings.HasPrefix(relativeOutputPath, ".."+string(filepath.Separator)))
	argument := os.Args[len(os.Args)-1]
	content := strings.Join(
		[]string{workingDirectory, os.Getenv(overlayInitValueEnv), argument},
		"|",
	)
	//nolint:gosec // G703: the helper verifies the output path is contained in its temporary workspace above.
	require.NoError(t, os.WriteFile(outputPath, []byte(content), 0o600))
}
