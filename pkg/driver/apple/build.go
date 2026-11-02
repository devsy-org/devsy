package apple

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/apple"
	"github.com/devsy-org/devsy/pkg/devcontainer/build"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
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
		BuiltLocally:  true,
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

	log.Debugf("found existing local image %s", imageName)
	recordImageReused(ctx)
	return &config.BuildInfo{
		ImageDetails:  imageDetails,
		ImageMetadata: req.ExtendedBuildInfo.MetadataConfig,
		ImageName:     imageName,
		PrebuildHash:  req.PrebuildHash,
		RegistryCache: req.Options.RegistryCache,
		Tags:          req.Options.Tag,
		BuiltLocally:  true,
	}, true
}

func (d *appleDriver) executeBuild(
	ctx context.Context,
	options *build.BuildOptions,
	platform string,
) error {
	args := buildArgs(options, platform)
	log.Debugf("building image with: container %s", redactArgs(args))

	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()
	stderrBuf := &bytes.Buffer{}

	run := startBuildRun(ctx, platform)
	streams := apple.Streams{
		Stdout: run.tee(writer, workspacelogs.StreamStdout),
		Stderr: io.MultiWriter(run.tee(writer, workspacelogs.StreamStderr), stderrBuf),
	}
	err := d.Apple.Run(ctx, args, streams)
	run.finish(ctx, err)
	if err != nil {
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

const buildPhase = "build"

// buildRun records one image build as an invocation in the run found in the
// context. A nil buildRun does nothing.
type buildRun struct {
	rec     *workspacelogs.Recorder
	id      string
	started time.Time
	writers []io.Closer
}

func buildKey(platform string) string {
	if platform == "" {
		return "apple"
	}

	return "apple " + platform
}

func startBuildRun(ctx context.Context, platform string) *buildRun {
	rec := workspacelogs.RecorderFrom(ctx)
	if rec == nil {
		return nil
	}
	b := &buildRun{rec: rec, id: workspacelogs.NewID(), started: time.Now()}
	rec.Invocation(workspacelogs.Invocation{
		ID: b.id, Phase: buildPhase, Key: buildKey(platform),
		State: workspacelogs.InvocationRunning,
	})

	return b
}

func recordImageReused(ctx context.Context) {
	rec := workspacelogs.RecorderFrom(ctx)
	if rec == nil {
		return
	}
	rec.Invocation(workspacelogs.Invocation{
		ID: workspacelogs.NewID(), Phase: buildPhase, Key: "apple",
		State: workspacelogs.InvocationSkipped, Reason: "image_reused",
	})
}

// tee records redacted output before forwarding to dst, so the text is kept
// regardless of the logger level.
func (b *buildRun) tee(dst io.Writer, stream workspacelogs.Stream) io.Writer {
	if b == nil {
		return dst
	}
	w := b.rec.Writer(b.id, stream)
	b.writers = append(b.writers, w)

	return io.MultiWriter(w, dst)
}

// finish flushes every writer, then records the terminal state for err.
func (b *buildRun) finish(ctx context.Context, err error) {
	if b == nil {
		return
	}
	for _, w := range b.writers {
		_ = w.Close()
	}
	inv := workspacelogs.Invocation{
		ID: b.id, Phase: buildPhase, State: workspacelogs.InvocationSucceeded,
		Duration: time.Since(b.started),
	}
	if err != nil {
		inv.State = workspacelogs.InvocationFailed
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			inv.State = workspacelogs.InvocationCanceled
		}
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			code := exitErr.ExitCode()
			inv.ExitCode = &code
		}
	}
	b.rec.Invocation(inv)
}
