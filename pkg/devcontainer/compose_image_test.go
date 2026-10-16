package devcontainer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	"github.com/devsy-org/devsy/pkg/compose"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/metadata"
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
	t.Setenv(composeDefaultPlatformEnv, "")
	if runtime.GOOS == goosWindows {
		t.Skip("Docker command fixture requires a POSIX shell")
	}
	command := filepath.Join(t.TempDir(), "docker-fixture")
	// #nosec G306 -- executable test-owned command fixture.
	require.NoError(t, os.WriteFile(command, []byte(`#!/bin/sh
case " $* " in
  *" inspect "*) echo '[{"Id":"sha256:cached-image","Os":"linux","Architecture":"amd64","Config":{}}]' ;;
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
			_, err := prepareComposeServiceImage(context.Background(), params)
			if test.wantPull {
				require.ErrorContains(t, err, "pull compose service image")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestComposeServicePlatform(t *testing.T) {
	t.Setenv(composeDefaultPlatformEnv, platformLinuxAMD64)
	for _, test := range []struct {
		name        string
		service     string
		environment []string
		want        string
	}{
		{"shell default", "", nil, platformLinuxAMD64},
		{"provider override", "", []string{composeDefaultPlatformEnv + "=" + platformLinuxARM64}, platformLinuxARM64},
		{
			"service override", platformLinuxAMD64,
			[]string{composeDefaultPlatformEnv + "=" + platformLinuxARM64},
			platformLinuxAMD64,
		},
		{"empty provider override", "", []string{composeDefaultPlatformEnv + "="}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, composeServicePlatform(
				&composetypes.ServiceConfig{Platform: test.service}, test.environment,
			))
		})
	}
}

func TestComposeCachedImagePlatform(t *testing.T) {
	if runtime.GOOS == goosWindows {
		t.Skip("Docker command fixture requires a POSIX shell")
	}
	t.Setenv(composeDefaultPlatformEnv, "")
	command := filepath.Join(t.TempDir(), "docker-fixture")
	// #nosec G306 -- executable test-owned command fixture.
	require.NoError(t, os.WriteFile(command, []byte(`#!/bin/sh
case " $* " in
  *" inspect "*) echo '[{"Id":"sha256:cached-image","Os":"linux","Architecture":"amd64","Config":{}}]' ;;
  *" pull "*) exit 47 ;;
  *) exit 48 ;;
esac
`), 0o700))
	for _, test := range []struct {
		name     string
		platform string
		policy   string
		wantErr  string
	}{
		{"matching cache", platformLinuxAMD64, "", ""},
		{"architecture alias", "linux/x86_64", "", ""},
		{"wrong platform is pulled", platformLinuxARM64, "", "pull compose service image"},
		{"never rejects wrong platform", platformLinuxARM64, composetypes.PullPolicyNever, "no cached variant"},
	} {
		t.Run(test.name, func(t *testing.T) {
			params := &buildAndExtendParams{
				composeHelper: &compose.ComposeHelper{
					Docker: &docker.DockerHelper{DockerCommand: command},
				},
				composeService: &composetypes.ServiceConfig{
					Image: exampleBaseImage, Platform: test.platform, PullPolicy: test.policy,
				},
				parsedConfig: &config.SubstitutedConfig{Config: &config.DevContainerConfig{}},
			}
			_, err := prepareComposeServiceImage(context.Background(), params)
			if test.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.wantErr)
			}
		})
	}
}

func TestComposePlatformPullIsVerified(t *testing.T) {
	if runtime.GOOS == goosWindows {
		t.Skip("Docker command fixture requires a POSIX shell")
	}
	t.Setenv(composeDefaultPlatformEnv, "")
	dir := t.TempDir()
	command := filepath.Join(dir, "docker-fixture")
	// #nosec G306 -- executable test-owned command fixture.
	require.NoError(t, os.WriteFile(command, []byte(`#!/bin/sh
case " $* " in
  *" inspect "*)
    arch=amd64
    if [ -f "$DEVSY_TEST_PULL_MARKER" ] && [ "$DEVSY_TEST_PULL_UPDATES" = yes ]; then arch=arm64; fi
    printf '[{"Id":"sha256:cached-image","Os":"linux","Architecture":"%s","Config":{}}]\n' "$arch"
    ;;
  *" pull "*)
    case " $* " in *" --platform linux/arm64 "*) ;; *) exit 48 ;; esac
    touch "$DEVSY_TEST_PULL_MARKER"
    ;;
  *) exit 49 ;;
esac
`), 0o700))
	for _, updates := range []string{"yes", "no"} {
		t.Run(updates, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "pulled")
			params := &buildAndExtendParams{
				composeHelper: &compose.ComposeHelper{Docker: &docker.DockerHelper{
					DockerCommand: command,
					Environment: []string{
						"DEVSY_TEST_PULL_MARKER=" + marker,
						"DEVSY_TEST_PULL_UPDATES=" + updates,
					},
				}},
				composeService: &composetypes.ServiceConfig{
					Image: exampleBaseImage, Platform: platformLinuxARM64,
				},
				parsedConfig: &config.SubstitutedConfig{Config: &config.DevContainerConfig{}},
			}
			_, err := prepareComposeServiceImage(context.Background(), params)
			require.FileExists(t, marker)
			if updates == "yes" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "no cached variant")
			}
		})
	}
}

func TestComposeMetadataUsesResolvedPlatform(t *testing.T) {
	const selectedArchitecture = "amd64"
	r := newTestRunner(&mockDriver{})
	r.imageBackend = &separateImages{err: errors.New("mutable image tag was inspected again")}
	details := &config.ImageDetails{
		ID: "sha256:index", OS: "linux", Architecture: selectedArchitecture,
		Config: config.ImageDetailsConfig{
			User: "selected-user",
			Labels: map[string]string{
				metadata.ImageMetadataLabel: `[{"postStartCommand":"echo selected-platform"}]`,
			},
		},
	}
	info, err := r.prepareComposeBuildInfo(
		context.Background(), &config.SubstitutionContext{},
		&composetypes.ServiceConfig{Image: exampleBaseImage}, "selected-stage", details,
	)
	require.NoError(t, err)
	require.Equal(t, "selected-user", info.imageBuildInfo.User)
	require.Equal(t, selectedArchitecture, info.imageBuildInfo.ImageDetails.Architecture)
	value, err := metadata.MarshalImageMetadata(info.imageBuildInfo.Metadata.Raw)
	require.NoError(t, err)
	require.Contains(t, string(value), "selected-platform")
	runtimeImage, err := resolveComposeRuntimeImage(context.Background(), nil, composeExtendResult{
		imageDetails: details,
	})
	require.NoError(t, err)
	require.Same(
		t,
		details,
		runtimeImage,
		"runtime pinning must not resolve the mutable source tag again",
	)
}
