package apple

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/devsy-org/devsy/pkg/apple"
	"github.com/devsy-org/devsy/pkg/devcontainer/build"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
)

func (d *appleDriver) BuildDevContainer(
	ctx context.Context,
	req driver.BuildRequest,
) (*config.BuildInfo, error) {
	imageName := build.GetImageName(req.LocalWorkspaceFolder, req.PrebuildHash)
	if req.Options.ImageName != "" {
		imageName = req.Options.ImageName
	}

	if info, found := d.resolveExistingImage(ctx, imageName, req); found {
		return info, nil
	}

	if req.Options.NoBuild {
		return nil, fmt.Errorf("cannot build in no-build mode when the image does not exist")
	}

	buildOptions, err := build.NewOptions(build.NewOptionsParams{
		DockerfilePath:    req.DockerfilePath,
		DockerfileContent: req.DockerfileContent,
		ParsedConfig:      req.ParsedConfig,
		ExtendedBuildInfo: req.ExtendedBuildInfo,
		ImageName:         imageName,
		Options:           req.Options,
		PrebuildHash:      req.PrebuildHash,
	})
	if err != nil {
		return nil, err
	}

	if err := d.Apple.EnsureBuilderRunning(ctx); err != nil {
		return nil, err
	}

	if err := d.executeBuild(ctx, buildOptions, req.Options.Platform); err != nil {
		return nil, err
	}

	imageDetails, err := d.Apple.InspectImage(ctx, imageName, false)
	if err != nil {
		return nil, fmt.Errorf("get image details: %w", err)
	}

	return &config.BuildInfo{
		ImageDetails:  imageDetails,
		ImageMetadata: req.ExtendedBuildInfo.MetadataConfig,
		ImageName:     imageName,
		PrebuildHash:  req.PrebuildHash,
		RegistryCache: req.Options.RegistryCache,
		Tags:          req.Options.Tag,
	}, nil
}

func (d *appleDriver) resolveExistingImage(
	ctx context.Context,
	imageName string,
	req driver.BuildRequest,
) (*config.BuildInfo, bool) {
	if req.Options.Repository != "" || req.Options.ForceBuild {
		return nil, false
	}

	imageDetails, err := d.Apple.InspectImage(ctx, imageName, false)
	if err != nil || imageDetails == nil {
		return nil, false
	}

	log.Infof("found existing local image %s", imageName)
	return &config.BuildInfo{
		ImageDetails:  imageDetails,
		ImageMetadata: req.ExtendedBuildInfo.MetadataConfig,
		ImageName:     imageName,
		PrebuildHash:  req.PrebuildHash,
		RegistryCache: req.Options.RegistryCache,
		Tags:          req.Options.Tag,
	}, true
}

func (d *appleDriver) executeBuild(
	ctx context.Context,
	options *build.BuildOptions,
	platform string,
) error {
	args := buildArgs(options, platform)
	log.Infof("building image with: container %s", redactArgs(args))

	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()
	stderrBuf := &bytes.Buffer{}

	streams := apple.Streams{Stdout: writer, Stderr: io.MultiWriter(writer, stderrBuf)}
	if err := d.Apple.Run(ctx, args, streams); err != nil {
		if stderrBuf.Len() > 0 {
			return fmt.Errorf(
				"failed to build image: %w: %s",
				err, strings.TrimSpace(stderrBuf.String()),
			)
		}
		return fmt.Errorf("failed to build image: %w", err)
	}
	return nil
}

// buildArgs assembles `container build` args; Apple's BuildKit builder accepts a
// Docker-like flag set but not buildx/registry-cache flags.
func buildArgs(options *build.BuildOptions, platform string) []string {
	args := []string{"build", "-f", options.Dockerfile}

	if options.NoCache {
		args = append(args, "--no-cache")
	}
	for _, img := range options.Images {
		args = append(args, "-t", img)
	}

	buildArgKeys := sortedKeys(options.BuildArgs)
	for _, k := range buildArgKeys {
		args = append(args, "--build-arg", k+"="+options.BuildArgs[k])
	}

	labelKeys := sortedKeys(options.Labels)
	for _, k := range labelKeys {
		args = append(args, "--label", k+"="+options.Labels[k])
	}

	if options.Target != "" {
		args = append(args, "--target", options.Target)
	}
	if platform != "" {
		args = append(args, "--platform", platform)
	}

	args = append(args, options.CliOpts...)
	args = append(args, options.Context)
	return args
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
