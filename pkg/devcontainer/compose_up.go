package devcontainer

import (
	"reflect"
	"strings"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	"github.com/devsy-org/devsy/pkg/compose"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"gopkg.in/yaml.v3"
)

// composeLabelEscaper escapes characters in label values that would otherwise
// trigger shell/compose variable interpolation. A literal replacer is used
// instead of a regex because the substitution is a fixed per-character mapping:
//   - "$" -> "$$" so compose does not expand it as a variable reference
//   - "'" -> "\'\'" so single quotes survive shell-quoted entrypoints
var composeLabelEscaper = strings.NewReplacer("$", "$$", "'", `\'\'`)

func escapeComposeLabelValue(value string) string {
	return composeLabelEscaper.Replace(value)
}

func (r *runner) extendedDockerComposeUp(params *composeUpParams) (string, error) {
	dockerComposeUpProject := r.generateDockerComposeUpProject(params)
	dockerComposeData, err := yaml.Marshal(dockerComposeUpProject)
	if err != nil {
		return "", err
	}

	dockerComposePath, err := r.writeComposeOverrideFile(
		FeaturesStartOverrideFilePrefix,
		dockerComposeData,
	)
	if err != nil {
		return "", err
	}

	log.Debugf(
		"Creating docker-compose up %s with content:\n %s",
		dockerComposePath,
		string(dockerComposeData),
	)

	return dockerComposePath, nil
}

func (r *runner) generateDockerComposeUpProject(params *composeUpParams) *composetypes.Project {
	mergedConfig := params.mergedConfig
	composeService := params.composeService

	userEntrypoint, userCommand := resolveServiceEntrypoint(
		mergedConfig,
		composeService,
		params.imageDetails,
	)

	overrideService := &composetypes.ServiceConfig{
		Name:        composeService.Name,
		Entrypoint:  buildOverrideEntrypoint(mergedConfig, userEntrypoint),
		Environment: mappingFromMap(mergedConfig.ContainerEnv),
		Init:        mergedConfig.Init,
		CapAdd:      mergedConfig.CapAdd,
		SecurityOpt: mergedConfig.SecurityOpt,
		Labels:      r.buildServiceLabels(params.additionalLabels),
	}

	if params.originalImageName != params.overrideImageName {
		overrideService.Image = params.overrideImageName
	}

	if !reflect.DeepEqual(userCommand, composeService.Command) {
		overrideService.Command = userCommand
	}

	if mergedConfig.ContainerUser != "" {
		overrideService.User = mergedConfig.ContainerUser
	}

	if mergedConfig.Privileged != nil {
		overrideService.Privileged = *mergedConfig.Privileged
	}

	gpuSupportEnabled := r.resolveComposeGPUAvailability(params.composeHelper)
	r.configureGPUResources(params.parsedConfig, gpuSupportEnabled, overrideService)

	for _, mount := range mergedConfig.Mounts {
		overrideService.Volumes = append(
			overrideService.Volumes,
			mountToServiceVolumeConfig(mount),
		)
	}

	project := &composetypes.Project{
		Services: map[string]composetypes.ServiceConfig{
			overrideService.Name: *overrideService,
		},
		Volumes: namedVolumesFromMounts(mergedConfig.Mounts),
	}

	return project
}

// resolveServiceEntrypoint determines the effective entrypoint and command for
// the overridden service. When OverrideCommand is set both are cleared so the
// devcontainer entrypoint takes over; otherwise the service values fall back to
// the image's own entrypoint and command.
func resolveServiceEntrypoint(
	mergedConfig *config.MergedDevContainerConfig,
	composeService *composetypes.ServiceConfig,
	imageDetails *config.ImageDetails,
) (entrypoint, command []string) {
	entrypoint = composeService.Entrypoint
	command = composeService.Command

	if mergedConfig.OverrideCommand != nil && *mergedConfig.OverrideCommand {
		return []string{}, []string{}
	}

	if len(entrypoint) == 0 {
		entrypoint = imageDetails.Config.Entrypoint
	}
	if len(command) == 0 {
		command = imageDetails.Config.Cmd
	}
	return entrypoint, command
}

// buildOverrideEntrypoint wraps the user entrypoint in the devcontainer startup
// shim that runs the configured entrypoint scripts before exec'ing the user
// command.
func buildOverrideEntrypoint(
	mergedConfig *config.MergedDevContainerConfig,
	userEntrypoint []string,
) composetypes.ShellCommand {
	entrypoint := composetypes.ShellCommand{
		"/bin/sh",
		"-c",
		`echo Container started
trap "exit 0" 15
` + strings.Join(mergedConfig.Entrypoints, "\n") + `
exec "$$@"
` + DefaultEntrypoint,
		"-",
	}
	return append(entrypoint, userEntrypoint...)
}

// buildServiceLabels assembles the labels for the overridden service. ID labels
// (or the default ID label) identify the workspace, and any additional labels
// are merged in. All values are escaped to prevent compose variable expansion.
func (r *runner) buildServiceLabels(additionalLabels map[string]string) composetypes.Labels {
	labels := composetypes.Labels{}
	if len(r.IDLabels) > 0 {
		for _, l := range r.IDLabels {
			k, v, _ := strings.Cut(l, "=")
			labels[k] = escapeComposeLabelValue(v)
		}
	} else {
		labels[config.DockerIDLabel] = r.ID
	}
	for k, v := range additionalLabels {
		labels.Add(k, escapeComposeLabelValue(v))
	}
	return labels
}

// namedVolumesFromMounts collects the named volumes referenced by the merged
// mounts so they can be declared at the project level. Returns nil when no
// volume-type mounts are present.
func namedVolumesFromMounts(mounts []*config.Mount) map[string]composetypes.VolumeConfig {
	var volumes map[string]composetypes.VolumeConfig
	for _, m := range mounts {
		// Only named volumes are declared at the project level; anonymous
		// volumes (empty source) stay service-scoped.
		if m.Type != composetypes.VolumeTypeVolume || m.Source == "" {
			continue
		}
		if volumes == nil {
			volumes = map[string]composetypes.VolumeConfig{}
		}
		volumes[m.Source] = composetypes.VolumeConfig{
			Name:     m.Source,
			External: composetypes.External(m.External),
		}
	}
	return volumes
}

func (r *runner) resolveComposeGPUAvailability(composeHelper *compose.ComposeHelper) bool {
	switch r.WorkspaceConfig.CLIOptions.GPUAvailability {
	case stringTrue:
		return true
	case stringFalse:
		return false
	default:
		available, _ := composeHelper.Docker.GPUSupportEnabled()
		return available
	}
}

func (r *runner) configureGPUResources(
	parsedConfig *config.SubstitutedConfig,
	gpuSupportEnabled bool,
	overrideService *composetypes.ServiceConfig,
) {
	if parsedConfig.Config.HostRequirements != nil {
		enableGPU, warnIfMissing := parsedConfig.Config.HostRequirements.ShouldEnableGPU(
			gpuSupportEnabled,
		)
		if enableGPU {
			overrideService.Deploy = &composetypes.DeployConfig{
				Resources: composetypes.Resources{
					Reservations: &composetypes.Resource{
						Devices: []composetypes.DeviceRequest{
							{
								Capabilities: []string{"gpu"},
							},
						},
					},
				},
			}
		}
		if warnIfMissing {
			log.Warn("GPU required but not available on host")
		}
	}
}
