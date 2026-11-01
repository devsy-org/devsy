package docker

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestInspectImageForPlatformKeepsIndexIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Docker command fixture requires a POSIX shell")
	}
	command := writeScript(t, t.TempDir(), "docker-fixture", `#!/bin/sh
case "$1" in
  inspect) cat <<'JSON'
[{"Id":"sha256:index","Os":"linux","Architecture":"arm64",
"Descriptor":{"mediaType":"application/vnd.oci.image.index.v1+json","digest":"sha256:index"},
"Config":{"User":"native"}}]
JSON
    ;;
  image) cat <<'JSON'
[{"Id":"sha256:amd64-manifest","Os":"linux","Architecture":"amd64",
"Descriptor":{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:amd64-manifest"},
"Config":{"User":"selected","Labels":{"variant":"amd64"}}}]
JSON
    ;;
  *) exit 48 ;;
esac
`)
	helper := &DockerHelper{DockerCommand: command}
	details, err := helper.InspectImageForPlatform(context.Background(), "test:base", "linux/amd64")
	require.NoError(t, err)
	require.Equal(t, "sha256:index", details.ID)
	require.Equal(t, "sha256:amd64-manifest", details.PlatformManifest)
	require.Equal(t, "selected", details.Config.User)
	require.Equal(t, "amd64", details.Config.Labels["variant"])
}

func TestInspectImagePlatformLegacyFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Docker command fixture requires a POSIX shell")
	}
	command := writeScript(t, t.TempDir(), "docker-fixture", `#!/bin/sh
case "$1" in
  inspect) echo '[{"Id":"sha256:legacy","Os":"linux","Architecture":"amd64","Config":{}}]' ;;
  image) echo 'unknown flag: --platform' >&2; exit 1 ;;
  *) exit 48 ;;
esac
`)
	helper := &DockerHelper{DockerCommand: command}
	_, err := helper.InspectImageForPlatform(context.Background(), "test:base", "linux/amd64")
	require.NoError(t, err)
	_, err = helper.InspectImageForPlatform(context.Background(), "test:base", "linux/arm64")
	require.ErrorIs(t, err, ErrImageNotFound)
}

func TestValidateImagePlatform(t *testing.T) {
	details := &config.ImageDetails{OS: "linux", Architecture: "arm64", Variant: "v8"}
	for _, platform := range []string{"linux/arm64", "linux/arm64/v8", "linux/aarch64"} {
		require.NoError(t, validateImagePlatform("test:base", platform, details))
	}
	require.ErrorIs(t, validateImagePlatform("test:base", "linux/amd64", details), ErrImageNotFound)
	require.Error(
		t,
		validateImagePlatform("test:base", "invalid/platform/too/many/components", details),
	)
	_, _, err := imageInspectionPlatform(&config.ImageDetails{
		Descriptor: &ocispec.Descriptor{MediaType: ocispec.MediaTypeImageIndex},
	}, "")
	require.ErrorIs(t, err, ErrImageNotFound)
}

func TestPlatformInspectionErrorClassification(t *testing.T) {
	for _, message := range []string{
		"unknown flag: --platform",
		`"--platform" requires api version 1.49, but the docker daemon api version is 1.48`,
	} {
		require.True(t, platformInspectionUnsupported(message))
		require.False(t, platformInspectionMiss(message))
	}
	for _, message := range []string{
		"image was found but its platform does not match the specified platform",
		"no matching manifest for linux/arm64",
		"no image found for platform linux/arm64",
	} {
		require.True(t, platformInspectionMiss(message))
		require.False(t, platformInspectionUnsupported(message))
	}
	for _, err := range []error{context.Canceled, errors.New("permission denied")} {
		require.False(t, platformInspectionMiss(err.Error()))
		require.False(t, platformInspectionUnsupported(err.Error()))
	}
}
