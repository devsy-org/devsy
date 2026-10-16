//go:build linux || darwin || unix

package up

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

func (tc *testContext) verifyCachedImagePlatforms(ctx context.Context) {
	const image = "ghcr.io/devsy-org/test-images/base:ubuntu" +
		"@sha256:4bcb1b466771b1ba1ea110e2a27daea2f6093f9527fb75ee59703ec89b5561cb"
	for _, platform := range []string{"linux/arm64", "linux/amd64"} {
		framework.ExpectNoError(tc.dockerHelper.Pull(ctx, docker.PullOptions{
			Image: image, Platform: platform, Stdout: io.Discard, Stderr: io.Discard,
		}))
		details, err := tc.dockerHelper.InspectImageForPlatform(ctx, image, platform)
		framework.ExpectNoError(err)
		gomega.Expect(details.OS + "/" + details.Architecture).To(gomega.Equal(platform))
	}
	tempDir, err := setupWorkspace(
		"tests/up-docker-compose/testdata/docker-compose-features-shared-image-a",
		tc.initialDir, tc.f,
	)
	framework.ExpectNoError(err)
	configDir := filepath.Join(tempDir, ".devcontainer")
	composeFile := "services:\n  app:\n    image: " + image +
		"\n    platform: linux/amd64\n    command: sleep infinity\n" +
		"    volumes:\n      - ..:/workspaces/project-a\n"
	framework.ExpectNoError(
		os.WriteFile(filepath.Join(configDir, "compose.yaml"), []byte(composeFile), 0o600),
	)
	framework.ExpectNoError(os.WriteFile(filepath.Join(configDir, "devcontainer.json"), []byte(`{
  "dockerComposeFile": ["compose.yaml", "compose.override.yaml"],
  "service": "app",
  "workspaceFolder": "/workspaces/project-a"
}`), 0o600))
	ginkgo.DeferCleanup(tc.f.DevsyWorkspaceDelete, tempDir)
	workspace, err := devsyUpAndFindWorkspace(ctx, tc.f, tempDir)
	framework.ExpectNoError(err)
	arch, err := tc.f.DevsySSHOnce(ctx, tempDir, "uname -m")
	framework.ExpectNoError(err)
	gomega.Expect(strings.TrimSpace(arch)).To(gomega.Equal("x86_64"))
	details, err := tc.getAppContainer(ctx, workspace)
	framework.ExpectNoError(err)
	selected, err := tc.dockerHelper.InspectImageForPlatform(ctx, image, "linux/amd64")
	framework.ExpectNoError(err)
	gomega.Expect(details.Image).To(gomega.Equal(selected.ID))
	if selected.PlatformManifest != "" {
		gomega.Expect(details.ImageManifestDescriptor).NotTo(gomega.BeNil())
		gomega.Expect(string(details.ImageManifestDescriptor.Digest)).
			To(gomega.Equal(selected.PlatformManifest))
	}
}
