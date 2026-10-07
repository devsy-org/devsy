package devcontainer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/api/pkg/devsy"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/stretchr/testify/require"
)

const (
	overlaySkipHelperEnv  = "DEVSY_OVERLAY_SKIP_HELPER"
	overlaySkipMarkerEnv  = "DEVSY_OVERLAY_SKIP_MARKER"
	overlaySkipFailureEnv = "DEVSY_OVERLAY_SKIP_FAILURE"
	overlaySkipBuildError = "intentional image inspection failure"
)

func TestUpSkipsOverlayInitializeCommandInPlatformAndRecovery(t *testing.T) {
	tests := []struct {
		name       string
		options    UpOptions
		wantReason string
	}{
		{
			name: "platform mode",
			options: UpOptions{CLIOptions: provider.CLIOptions{
				Platform: devsy.PlatformOptions{Enabled: true},
			}},
			wantReason: "platform mode",
		},
		{
			name:       "recovery mode",
			options:    UpOptions{CLIOptions: provider.CLIOptions{Recovery: true}},
			wantReason: "recovery mode",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertUpSkipsOverlayInitializeCommand(t, test.options, test.wantReason)
		})
	}
}

func assertUpSkipsOverlayInitializeCommand(t *testing.T, options UpOptions, wantReason string) {
	t.Helper()

	workspace := t.TempDir()
	basePath := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	overlayPath := filepath.Join(workspace, "overlay.json")
	markerPath := filepath.Join(workspace, "initialize-ran")
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0o750))
	writeJSONConfig(t, basePath, map[string]any{overlayImageKey: exampleBaseImage})
	executable, err := os.Executable()
	require.NoError(t, err)
	writeJSONConfig(t, overlayPath, map[string]any{
		overlayInitializeCommandKey: []string{
			executable,
			"-test.run=^TestOverlaySkipInitializeHelperProcess$",
		},
	})
	t.Setenv(overlaySkipHelperEnv, "1")
	t.Setenv(overlaySkipMarkerEnv, markerPath)

	buildFailure := errors.New(overlaySkipBuildError)
	d := &mockDriver{}
	r := newRunnerAt(workspace)
	r.driver = d
	r.imageBackend = &overlaySkipImageBackend{separateImages: separateImages{err: buildFailure}}
	options.ExtraDevContainerPath = overlayPath
	reporter := status.NewMemoryReporter()

	_, err = r.Up(context.Background(), options, 0, reporter)
	require.ErrorIs(t, err, buildFailure)
	require.Equal(t, []status.Event{
		{
			Phase: status.PhaseInitializeCommand,
			Step:  wantReason,
			State: status.StateSkipped,
		},
	}, initializeCommandEvents(reporter.Events()))
	_, err = os.Stat(markerPath)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.False(t, d.stopCalled)
	require.False(t, d.deleteCalled)
}

func TestUpInitializeCommandFailurePreventsBuildDispatch(t *testing.T) {
	workspace := t.TempDir()
	basePath := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	overlayPath := filepath.Join(workspace, "overlay.json")
	markerPath := filepath.Join(workspace, "initialize-ran")
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0o750))
	writeJSONConfig(t, basePath, map[string]any{overlayImageKey: exampleBaseImage})
	executable, err := os.Executable()
	require.NoError(t, err)
	writeJSONConfig(t, overlayPath, map[string]any{
		overlayInitializeCommandKey: []string{
			executable,
			"-test.run=^TestOverlaySkipInitializeHelperProcess$",
		},
	})
	t.Setenv(overlaySkipHelperEnv, "1")
	t.Setenv(overlaySkipMarkerEnv, markerPath)
	t.Setenv(overlaySkipFailureEnv, "1")

	d := &mockDriver{}
	r := newRunnerAt(workspace)
	r.driver = d
	imageBackend := &overlaySkipImageBackend{
		separateImages: separateImages{err: errors.New(overlaySkipBuildError)},
	}
	r.imageBackend = imageBackend
	options := UpOptions{CLIOptions: provider.CLIOptions{ExtraDevContainerPath: overlayPath}}

	_, err = r.Up(context.Background(), options, 0, status.NewMemoryReporter())
	require.ErrorContains(t, err, "initialize command")
	require.Zero(t, imageBackend.inspectCalls, "build dispatch must not inspect the image")
	_, err = os.Stat(markerPath)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.False(t, d.stopCalled)
	require.False(t, d.deleteCalled)
}

func initializeCommandEvents(events []status.Event) []status.Event {
	var initializeEvents []status.Event
	for _, event := range events {
		if event.Phase == status.PhaseInitializeCommand {
			initializeEvents = append(initializeEvents, event)
		}
	}
	return initializeEvents
}

type overlaySkipImageBackend struct {
	separateImages
	inspectCalls int
}

func (b *overlaySkipImageBackend) InspectImage(
	_ context.Context,
	_ string,
) (*config.ImageDetails, error) {
	b.inspectCalls++
	return nil, b.err
}

func TestOverlaySkipInitializeHelperProcess(t *testing.T) {
	if os.Getenv(overlaySkipHelperEnv) != "1" {
		return
	}
	if os.Getenv(overlaySkipFailureEnv) == "1" {
		t.Fatal("intentional initialize hook failure")
	}
	// #nosec G703 -- The test parent passes a marker path rooted in t.TempDir.
	if err := os.WriteFile(os.Getenv(overlaySkipMarkerEnv), []byte("ran"), 0o600); err != nil {
		t.Fatal(err)
	}
}
