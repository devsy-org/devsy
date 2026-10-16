package devcontainer

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	"github.com/devsy-org/devsy/pkg/compose"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/stretchr/testify/require"
)

func TestComposeImagePullPolicy(t *testing.T) {
	const latest = "example/base:latest"
	const daily = "daily"
	now := time.Date(2026, time.October, 15, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		policy  string
		image   string
		present bool
		age     time.Duration
		want    bool
	}{
		{"cached pinned tag", "", exampleBaseImage, true, 0, false},
		{"missing image", "", exampleBaseImage, false, 0, true},
		{"latest", "", latest, true, 0, true},
		{"implicit latest", "", "example/base", true, 0, true},
		{"never", composetypes.PullPolicyNever, latest, false, 0, false},
		{"always", composetypes.PullPolicyAlways, exampleBaseImage, true, 0, true},
		{"fresh daily", daily, exampleBaseImage, true, time.Hour, false},
		{"expired daily", daily, exampleBaseImage, true, 25 * time.Hour, true},
		{"missing daily", daily, exampleBaseImage, false, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var details *config.ImageDetails
			if test.present {
				details = &config.ImageDetails{
					Metadata: &config.ImageDetailsMetadata{LastTagTime: now.Add(-test.age)},
				}
			}
			pull, err := composeImageNeedsPull(
				&composetypes.ServiceConfig{Image: test.image, PullPolicy: test.policy},
				details,
				now,
			)
			require.NoError(t, err)
			require.Equal(t, test.want, pull)
		})
	}
}

func TestComposeServiceImageResolution(t *testing.T) {
	const localLatest = "local-base:latest"
	if runtime.GOOS == goosWindows {
		t.Skip("Docker command fixture requires a POSIX shell")
	}
	command := filepath.Join(t.TempDir(), "docker-fixture")
	// #nosec G306 -- executable test-owned command fixture.
	require.NoError(t, os.WriteFile(command, []byte(`#!/bin/sh
case " $* " in
  *" inspect "*) echo '[{"Id":"sha256:cached-image","Config":{}}]' ;;
  *" pull "*) exit 47 ;;
  *) exit 48 ;;
esac
`), 0o700))
	for _, test := range []struct {
		name     string
		image    string
		policy   string
		features bool
		pull     bool
		wantPull bool
	}{
		{"image-only never ignores build pull flag", exampleBaseImage, composetypes.PullPolicyNever, false, true, false},
		{"feature build pull flag refreshes base", exampleBaseImage, composetypes.PullPolicyNever, true, true, true},
		{"feature build reuses local latest by default", localLatest, "", true, false, false},
		{"feature build pull flag refreshes latest", localLatest, "", true, true, true},
		{"feature build honors explicit always", localLatest, composetypes.PullPolicyAlways, true, false, true},
		{"image-only latest retains compose policy", localLatest, "", false, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			devcontainer := &config.DevContainerConfig{}
			if test.features {
				devcontainer.Features = map[string]any{composeFeatureTestID: map[string]any{}}
			}
			params := &buildAndExtendParams{
				composeHelper: &compose.ComposeHelper{
					Docker: &docker.DockerHelper{DockerCommand: command},
				},
				composeService: &composetypes.ServiceConfig{
					Image: test.image, PullPolicy: test.policy,
				},
				parsedConfig: &config.SubstitutedConfig{Config: devcontainer},
				pull:         test.pull,
			}
			err := prepareComposeServiceImage(context.Background(), params)
			if test.wantPull {
				require.ErrorContains(t, err, "pull compose service image")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
