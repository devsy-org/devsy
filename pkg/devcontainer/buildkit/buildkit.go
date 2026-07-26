package buildkit

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/pkg/devcontainer/build"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/docker/cli/cli/config/configfile"
	buildkit "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/tonistiigi/fsutil"
)

func Build(
	ctx context.Context,
	client *buildkit.Client,
	writer io.Writer,
	platform string,
	options *build.BuildOptions,
) error {
	solveOptions, err := buildkitSolveOpt(platform, options)
	if err != nil {
		return err
	}

	pw, err := NewPrinter(ctx, writer)
	if err != nil {
		return err
	}

	// build
	_, err = client.Solve(ctx, nil, solveOptions, pw.Status())
	return err
}

func buildkitSolveOpt(platform string, options *build.BuildOptions) (buildkit.SolveOpt, error) {
	dockerConfig, err := docker.LoadDockerConfig()
	if err != nil {
		return buildkit.SolveOpt{}, err
	}

	cacheFrom, cacheTo, err := setupCache(options)
	if err != nil {
		return buildkit.SolveOpt{}, err
	}

	attachable, err := buildkitAttachables(dockerConfig, options)
	if err != nil {
		return buildkit.SolveOpt{}, err
	}

	solveOptions := buildkit.SolveOpt{
		Frontend: "dockerfile.v0",
		FrontendAttrs: map[string]string{
			"filename": filepath.Base(options.Dockerfile),
			"context":  options.Context,
		},
		Session:      attachable,
		CacheImports: cacheFrom,
		CacheExports: cacheTo,
		LocalMounts:  map[string]fsutil.FS{},
	}

	// set options target
	if options.Target != "" {
		solveOptions.FrontendAttrs["target"] = options.Target
	}

	// add platforms
	if platform != "" {
		solveOptions.FrontendAttrs["platform"] = platform
	}

	if err := buildkitLocalMounts(&solveOptions, options); err != nil {
		return buildkit.SolveOpt{}, err
	}
	if err := buildkitMultiContexts(&solveOptions, options); err != nil {
		return buildkit.SolveOpt{}, err
	}

	buildkitExports(&solveOptions, options)
	buildkitFrontendExtras(&solveOptions, options)

	// add additional build cli options
	// TODO: convert options.CliOpts into a solveOptions.FrontendAttr

	return solveOptions, nil
}

func buildkitAttachables(
	dockerConfig *configfile.ConfigFile,
	options *build.BuildOptions,
) ([]session.Attachable, error) {
	attachable := []session.Attachable{
		authprovider.NewDockerAuthProvider(
			authprovider.DockerAuthProviderConfig{
				AuthConfigProvider: authprovider.LoadAuthConfig(dockerConfig),
			},
		),
	}

	secretsAttachable, err := buildSecretsAttachable(options.BuildSecrets)
	if err != nil {
		return nil, err
	}
	if secretsAttachable != nil {
		attachable = append(attachable, secretsAttachable)
	}

	return attachable, nil
}

func buildkitLocalMounts(solveOptions *buildkit.SolveOpt, options *build.BuildOptions) error {
	contextFS, err := fsutil.NewFS(options.Context)
	if err != nil {
		return fmt.Errorf("failed to create build context fs: %w", err)
	}
	solveOptions.LocalMounts["context"] = contextFS
	dockerfileFS, err := fsutil.NewFS(filepath.Dir(options.Dockerfile))
	if err != nil {
		return fmt.Errorf("failed to create dockerfile fs: %w", err)
	}
	solveOptions.LocalMounts["dockerfile"] = dockerfileFS
	return nil
}

func buildkitMultiContexts(solveOptions *buildkit.SolveOpt, options *build.BuildOptions) error {
	for k, v := range options.Contexts {
		st, err := os.Stat(v)
		if err != nil {
			return fmt.Errorf("failed to get build context %v: %w", k, err)
		}
		if !st.IsDir() {
			return fmt.Errorf("build context %q is not a directory", v)
		}
		localName := k
		if k == "context" || k == "dockerfile" {
			localName = "_" + k // underscore to avoid collisions
		}
		contextMountFS, err := fsutil.NewFS(v)
		if err != nil {
			return fmt.Errorf("failed to create fs for build context %v: %w", k, err)
		}
		solveOptions.LocalMounts[localName] = contextMountFS
		solveOptions.FrontendAttrs["context:"+k] = "local:" + localName
	}
	return nil
}

func buildkitExports(solveOptions *buildkit.SolveOpt, options *build.BuildOptions) {
	// load?
	if options.Load {
		solveOptions.Exports = append(solveOptions.Exports, buildkit.ExportEntry{
			Type: "moby",
			Attrs: map[string]string{
				"name": strings.Join(options.Images, ","),
			},
		})
	} else if options.Push {
		solveOptions.Exports = append(solveOptions.Exports, buildkit.ExportEntry{
			Type: "image",
			Attrs: map[string]string{
				"name":           strings.Join(options.Images, ","),
				"name-canonical": "",
				"push":           "true",
			},
		})
	}
}

func buildkitFrontendExtras(solveOptions *buildkit.SolveOpt, options *build.BuildOptions) {
	// add labels
	for k, v := range options.Labels {
		solveOptions.FrontendAttrs["label:"+k] = v
	}

	// add build args
	for key, value := range options.BuildArgs {
		solveOptions.FrontendAttrs["build-arg:"+key] = value
	}
}
