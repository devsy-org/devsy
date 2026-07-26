package apple

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
)

// buildRunArgs assembles `container run` args, emitting only flags Apple supports
// (Docker/Podman-only flags like --security-opt/--gpus/--userns are omitted).
func (d *appleDriver) buildRunArgs(params *driver.RunImageDevContainerParams) []string {
	args := []string{"run", "-d"}

	if d.Rosetta {
		args = append(args, "--rosetta")
	}

	warnUnsupportedGPU(params.ParsedConfig)
	args = d.addPorts(args, params.ParsedConfig)
	args = d.addWorkspaceMount(args, params.Options)
	args = addUser(args, params.Options)
	args = addEnv(args, params.Options)
	args = addInit(args, params.Options)
	args = addCapabilities(args, params.Options)
	args = addMounts(args, params.Options)
	args = d.addLabels(args, params.WorkspaceID, params.Options)
	args = append(args, params.ParsedConfig.RunArgs...)
	args = addPlatform(args, params.ParsedConfig, params.Options)
	args = addEntrypoint(args, params.Options)

	args = append(args, params.Options.Image)
	args = append(args, params.Options.Cmd...)
	return args
}

func (d *appleDriver) addPorts(args []string, parsedConfig *config.DevContainerConfig) []string {
	for _, appPort := range parsedConfig.AppPort {
		if intPort, err := strconv.Atoi(appPort); err == nil {
			args = append(args, "-p", fmt.Sprintf("%d:%d", intPort, intPort))
		} else {
			args = append(args, "-p", appPort)
		}
	}
	return args
}

func (d *appleDriver) addWorkspaceMount(args []string, options *driver.RunOptions) []string {
	if options.WorkspaceMount != nil {
		args = append(args, "--mount", cleanMount(options.WorkspaceMount.String()))
	}
	return args
}

func addUser(args []string, options *driver.RunOptions) []string {
	if options.User != "" {
		args = append(args, "-u", options.User)
	}
	return args
}

func addEnv(args []string, options *driver.RunOptions) []string {
	for k, v := range options.Env {
		args = append(args, "-e", k+"="+v)
	}
	return args
}

func addInit(args []string, options *driver.RunOptions) []string {
	if options.Init != nil && *options.Init {
		args = append(args, "--init")
	}
	return args
}

func addCapabilities(args []string, options *driver.RunOptions) []string {
	for _, capAdd := range options.CapAdd {
		args = append(args, "--cap-add", capAdd)
	}
	return args
}

func addMounts(args []string, options *driver.RunOptions) []string {
	for _, mount := range options.Mounts {
		if mount.Type == "bind" && mount.Source != "" {
			if _, err := os.Stat(mount.Source); os.IsNotExist(err) {
				log.Warnf("bind mount source path does not exist, skipping: %s", mount.Source)
				continue
			}
		}
		args = append(args, "--mount", cleanMount(mount.String()))
	}
	return args
}

func (d *appleDriver) addLabels(
	args []string,
	workspaceID string,
	options *driver.RunOptions,
) []string {
	labels := append(config.GetIDLabels(workspaceID, d.IDLabels), options.Labels...)
	for _, label := range labels {
		args = append(args, "-l", label)
	}
	return args
}

func addEntrypoint(args []string, options *driver.RunOptions) []string {
	if options.Entrypoint != "" {
		args = append(args, "--entrypoint", options.Entrypoint)
	}
	return args
}

// warnUnsupportedGPU warns when a GPU is required, since Apple's `container` has
// no GPU passthrough (matching how the docker driver warns on a missing GPU).
func warnUnsupportedGPU(parsedConfig *config.DevContainerConfig) {
	if parsedConfig == nil {
		return
	}
	if _, warnIfMissing := parsedConfig.HostRequirements.ShouldEnableGPU(false); warnIfMissing {
		log.Warn(
			"devcontainer requires a GPU, but the apple provider does not support GPU passthrough",
		)
	}
}

func addPlatform(
	args []string,
	parsedConfig *config.DevContainerConfig,
	options *driver.RunOptions,
) []string {
	if options.Platform == "" {
		return args
	}
	for _, a := range parsedConfig.RunArgs {
		if a == "--platform" || strings.HasPrefix(a, "--platform=") {
			return args // explicit config wins
		}
	}
	return append(args, "--platform="+options.Platform)
}

// redactArgs masks the values of secret-bearing flags (-e, --build-arg) for
// logging, so env vars and build args carrying tokens are not written in plaintext.
func redactArgs(args []string) string {
	redacted := make([]string, len(args))
	copy(redacted, args)
	for i := 0; i < len(redacted)-1; i++ {
		if redacted[i] != "-e" && redacted[i] != "--build-arg" {
			continue
		}
		if key, _, found := strings.Cut(redacted[i+1], "="); found {
			redacted[i+1] = key + "=****"
		}
	}
	return strings.Join(redacted, " ")
}

// cleanMount strips Docker Desktop-only mount options (consistency=, bind-create-src=)
// that Apple's `container` rejects.
func cleanMount(mount string) string {
	parts := strings.Split(mount, ",")
	kept := parts[:0]
	for _, part := range parts {
		if strings.HasPrefix(part, "consistency=") || strings.HasPrefix(part, "bind-create-src=") {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, ",")
}
