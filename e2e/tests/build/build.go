package build

import (
	"context"
	"os"
	"path/filepath"
	"runtime"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/devcontainer/build"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/dockerfile"
	"github.com/onsi/ginkgo/v2"
)

const (
	prebuildRepoName = "test-repo"
	osWindows        = "windows"
)

func prepareDockerfileContent(dockerfilePath string) (string, error) {
	dockerfileContent, err := os.ReadFile(dockerfilePath) // #nosec G304 -- test file path
	if err != nil {
		return "", err
	}
	_, modifiedDockerfileContents, err := dockerfile.EnsureFinalStageName(
		string(dockerfileContent),
		config.DockerfileDefaultTarget,
	)
	if err != nil {
		return "", err
	}
	contentToParse := modifiedDockerfileContents
	if contentToParse == "" {
		contentToParse = string(dockerfileContent)
	}
	return contentToParse, nil
}

func getDevcontainerConfig(dir string) *config.DevContainerConfig {
	return &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			Name: "Build Example",
		},
		DevContainerActions: config.DevContainerActions{},
		NonComposeBase:      config.NonComposeBase{},
		ImageContainer:      config.ImageContainer{},
		ComposeContainer:    config.ComposeContainer{},
		DockerfileContainer: config.DockerfileContainer{
			Build: &config.ConfigBuildOptions{
				Dockerfile: "Dockerfile",
				Context:    ".",
				Options:    []string{"--label=test=VALUE"},
			},
		},
		Origin: dir + "/.devcontainer/devcontainer.json",
	}
}

var _ = ginkgo.Describe("devsy build test suite", ginkgo.Label("build"), ginkgo.Ordered, func() {
	var initialDir string
	var dockerHelper *docker.DockerHelper

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
		dockerHelper = &docker.DockerHelper{DockerCommand: "docker"}
	})

	ginkgo.It("build docker buildx",
		ginkgo.SpecTimeout(framework.TimeoutShort()),
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")
			tempDir, err := framework.CopyToTempDir("tests/build/testdata/docker")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

			_ = f.DevsyProviderDelete(ctx, "docker")
			err = f.DevsyProviderAdd(ctx, "docker")
			framework.ExpectNoError(err)
			err = f.DevsyProviderUse(ctx, "docker")
			framework.ExpectNoError(err)

			cfg := getDevcontainerConfig(tempDir)

			dockerfilePath := tempDir + "/.devcontainer/Dockerfile"
			contentToParse, err := prepareDockerfileContent(dockerfilePath)
			framework.ExpectNoError(err)

			// do the build
			platforms := "linux/amd64,linux/arm64"
			err = f.DevsyBuild(
				ctx,
				tempDir,
				"--force-build",
				"--platform",
				platforms,
				"--repository",
				prebuildRepoName,
				"--skip-push",
			)
			framework.ExpectNoError(err)

			// parse the dockerfile
			file, err := dockerfile.Parse(contentToParse)
			framework.ExpectNoError(err)
			info := &config.ImageBuildInfo{Dockerfile: file}

			// make sure images are there
			prebuildHash, err := config.CalculatePrebuildHash(config.PrebuildHashParams{
				Config:            cfg,
				Platform:          "linux/amd64",
				Architecture:      "amd64",
				ContextPath:       filepath.Dir(cfg.Origin),
				DockerfilePath:    dockerfilePath,
				DockerfileContent: contentToParse,
				BuildInfo:         info,
			})
			framework.ExpectNoError(err)
			_, err = dockerHelper.InspectImage(ctx, prebuildRepoName+":"+prebuildHash, false)
			framework.ExpectNoError(err)

			prebuildHash, err = config.CalculatePrebuildHash(config.PrebuildHashParams{
				Config:            cfg,
				Platform:          "linux/arm64",
				Architecture:      "arm64",
				ContextPath:       filepath.Dir(cfg.Origin),
				DockerfilePath:    dockerfilePath,
				DockerfileContent: contentToParse,
				BuildInfo:         info,
			})
			framework.ExpectNoError(err)

			details, err := dockerHelper.InspectImage(ctx, prebuildRepoName+":"+prebuildHash, false)
			framework.ExpectNoError(err)
			framework.ExpectEqual(
				details.Config.Labels["test"],
				"VALUE",
				"should contain test label",
			)
		})

	ginkgo.It(
		"should build image without repository specified if skip-push flag is set",
		ginkgo.SpecTimeout(framework.TimeoutShort()),
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")
			tempDir, err := framework.CopyToTempDir("tests/build/testdata/docker")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

			_ = f.DevsyProviderDelete(ctx, "docker")
			err = f.DevsyProviderAdd(ctx, "docker")
			framework.ExpectNoError(err)
			err = f.DevsyProviderUse(ctx, "docker")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

			cfg := getDevcontainerConfig(tempDir)

			dockerfilePath := tempDir + "/.devcontainer/Dockerfile"
			contentToParse, err := prepareDockerfileContent(dockerfilePath)
			framework.ExpectNoError(err)

			// do the build
			err = f.DevsyBuild(ctx, tempDir, "--skip-push")
			framework.ExpectNoError(err)

			// parse the dockerfile
			file, err := dockerfile.Parse(contentToParse)
			framework.ExpectNoError(err)
			info := &config.ImageBuildInfo{Dockerfile: file}

			// make sure images are there
			prebuildHash, err := config.CalculatePrebuildHash(config.PrebuildHashParams{
				Config:            cfg,
				Platform:          "linux/" + runtime.GOARCH,
				Architecture:      runtime.GOARCH,
				ContextPath:       filepath.Dir(cfg.Origin),
				DockerfilePath:    dockerfilePath,
				DockerfileContent: contentToParse,
				BuildInfo:         info,
			})
			framework.ExpectNoError(err)
			_, err = dockerHelper.InspectImage(
				ctx,
				build.GetImageName(tempDir, prebuildHash),
				false,
			)
			framework.ExpectNoError(err)
		},
	)

	ginkgo.It(
		"should build the image of the referenced service from the docker compose file",
		ginkgo.SpecTimeout(framework.TimeoutShort()),
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")
			tempDir, err := framework.CopyToTempDir("tests/build/testdata/docker-compose")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

			_ = f.DevsyProviderDelete(ctx, "docker")
			err = f.DevsyProviderAdd(ctx, "docker")
			framework.ExpectNoError(err)
			err = f.DevsyProviderUse(ctx, "docker")
			framework.ExpectNoError(err)

			ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

			prebuildRepo := prebuildRepoName

			// do the build
			err = f.DevsyBuild(ctx, tempDir, "--repository", prebuildRepo, "--skip-push")
			framework.ExpectNoError(err)
		},
	)

	ginkgo.It(
		"should build docker-compose with features when build context differs from devcontainer location",
		ginkgo.SpecTimeout(framework.TimeoutShort()),
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")
			tempDir, err := framework.CopyToTempDir(
				"tests/build/testdata/docker-compose-features-context",
			)
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

			_ = f.DevsyProviderDelete(ctx, "docker")
			err = f.DevsyProviderAdd(ctx, "docker")
			framework.ExpectNoError(err)
			err = f.DevsyProviderUse(ctx, "docker")
			framework.ExpectNoError(err)

			ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

			err = f.DevsyBuild(ctx, tempDir, "--skip-push")
			framework.ExpectNoError(err)
		},
	)

	ginkgo.It("should build with --cache-from flag",
		ginkgo.SpecTimeout(framework.TimeoutShort()),
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")
			tempDir, err := framework.CopyToTempDir("tests/build/testdata/docker")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

			_ = f.DevsyProviderDelete(ctx, "docker")
			err = f.DevsyProviderAdd(ctx, "docker")
			framework.ExpectNoError(err)
			err = f.DevsyProviderUse(ctx, "docker")
			framework.ExpectNoError(err)

			ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

			err = f.DevsyBuild(
				ctx,
				tempDir,
				"--skip-push",
				"--cache-from",
				"ghcr.io/devsy-org/test-images/base:alpine",
			)
			framework.ExpectNoError(err)
		})

	ginkgo.It("build docker internal buildkit",
		ginkgo.SpecTimeout(framework.TimeoutShort()),
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")
			tempDir, err := framework.CopyToTempDir("tests/build/testdata/docker")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

			_ = f.DevsyProviderDelete(ctx, "docker")
			err = f.DevsyProviderAdd(ctx, "docker")
			framework.ExpectNoError(err)
			err = f.DevsyProviderUse(ctx, "docker")
			framework.ExpectNoError(err)

			ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

			cfg := getDevcontainerConfig(tempDir)

			dockerfilePath := tempDir + "/.devcontainer/Dockerfile"
			contentToParse, err := prepareDockerfileContent(dockerfilePath)
			framework.ExpectNoError(err)

			prebuildRepo := prebuildRepoName

			// do the build
			err = f.DevsyBuild(
				ctx,
				tempDir,
				"--force-build",
				"--force-internal-buildkit",
				"--repository",
				prebuildRepo,
				"--skip-push",
			)
			framework.ExpectNoError(err)

			// parse the dockerfile
			file, err := dockerfile.Parse(contentToParse)
			framework.ExpectNoError(err)
			info := &config.ImageBuildInfo{Dockerfile: file}

			// make sure images are there
			prebuildHash, err := config.CalculatePrebuildHash(config.PrebuildHashParams{
				Config:            cfg,
				Platform:          "linux/" + runtime.GOARCH,
				Architecture:      runtime.GOARCH,
				ContextPath:       filepath.Dir(cfg.Origin),
				DockerfilePath:    dockerfilePath,
				DockerfileContent: contentToParse,
				BuildInfo:         info,
			})
			framework.ExpectNoError(err)

			_, err = dockerHelper.InspectImage(ctx, prebuildRepo+":"+prebuildHash, false)
			framework.ExpectNoError(err)
		})

	ginkgo.It("build kubernetes dockerless",
		ginkgo.SpecTimeout(framework.TimeoutShort()),
		func(ctx context.Context) {
			if runtime.GOOS == osWindows {
				ginkgo.Skip("skipping on windows")
			}

			f := framework.NewDefaultFramework(initialDir + "/bin")
			tempDir, err := framework.CopyToTempDir("tests/build/testdata/kubernetes")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

			_ = f.DevsyProviderDelete(ctx, "kubernetes")
			err = f.DevsyProviderAdd(ctx, "kubernetes")
			framework.ExpectNoError(err)
			err = f.DevsyProviderUse(
				ctx,
				"kubernetes",
				"-o",
				"KUBERNETES_NAMESPACE=devsy",
			)
			framework.ExpectNoError(err)

			ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

			// do the up
			err = f.DevsyUp(ctx, tempDir)
			framework.ExpectNoError(err)

			// check if ssh works
			out, err := f.DevsySSH(ctx, tempDir, "echo -n $MY_TEST")
			framework.ExpectNoError(err)
			framework.ExpectEqual(out, "test456", "should contain my-test")
		})

	ginkgo.It("rebuild kubernetes dockerless",
		ginkgo.SpecTimeout(framework.TimeoutShort()),
		func(ctx context.Context) {
			validateKubernetesDeploymentWithoutDocker(
				ctx,
				initialDir,
				func(ctx context.Context, f *framework.Framework, tempDir string) error {
					return f.DevsyUpRecreate(ctx, tempDir)
				},
			)
		})

	ginkgo.It("reset kubernetes dockerless",
		ginkgo.SpecTimeout(framework.TimeoutShort()),
		func(ctx context.Context) {
			validateKubernetesDeploymentWithoutDocker(
				ctx,
				initialDir,
				func(ctx context.Context, f *framework.Framework, tempDir string) error {
					return f.DevsyUpReset(ctx, tempDir)
				},
			)
		})
})

func validateKubernetesDeploymentWithoutDocker(
	ctx context.Context,
	initialDir string,
	action func(context.Context, *framework.Framework, string) error,
) {
	if runtime.GOOS == osWindows {
		ginkgo.Skip("skipping on Windows")
	}

	f := framework.NewDefaultFramework(initialDir + "/bin")
	tempDir, err := framework.CopyToTempDir("tests/build/testdata/kubernetes")
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

	_ = f.DevsyProviderDelete(ctx, "kubernetes")
	err = f.DevsyProviderAdd(ctx, "kubernetes")
	framework.ExpectNoError(err)
	err = f.DevsyProviderUse(
		ctx,
		"kubernetes",
		"-o",
		"KUBERNETES_NAMESPACE=devsy",
	)
	framework.ExpectNoError(err)

	ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

	err = f.DevsyUp(ctx, tempDir)
	framework.ExpectNoError(err)

	_, err = f.DevsySSH(ctx, tempDir, "touch /workspaces/"+filepath.Base(tempDir)+"/DATA")
	framework.ExpectNoError(err)
	_, err = f.DevsySSH(ctx, tempDir, "touch /ROOTFS")
	framework.ExpectNoError(err)

	err = action(ctx, f, tempDir)
	framework.ExpectNoError(err)

	_, err = f.DevsySSH(ctx, tempDir, "ls /workspaces/"+filepath.Base(tempDir)+"/DATA")
	framework.ExpectNoError(err)
	_, err = f.DevsySSH(ctx, tempDir, "ls /ROOTFS")
	framework.ExpectError(err)
}
