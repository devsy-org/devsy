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

func TestComposePullFlagOnlyRefreshesFeatureBuildBase(t *testing.T) {
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
	params := &buildAndExtendParams{
		composeHelper: &compose.ComposeHelper{Docker: &docker.DockerHelper{DockerCommand: command}},
		composeService: &composetypes.ServiceConfig{
			Image: exampleBaseImage, PullPolicy: composetypes.PullPolicyNever,
		},
		parsedConfig: &config.SubstitutedConfig{Config: &config.DevContainerConfig{}},
		pull:         true,
	}
	require.NoError(t, prepareComposeServiceImage(context.Background(), params))
	params.parsedConfig.Config.Features = map[string]any{composeFeatureTestID: map[string]any{}}
	require.ErrorContains(
		t,
		prepareComposeServiceImage(context.Background(), params),
		"pull compose service image",
	)
}
