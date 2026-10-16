package devcontainer

import (
	"context"
	"fmt"
	"strings"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
)

// Resume uses start rather than up: Compose must not replace a stopped instance
// because a source file, image tag, or generated override changed since creation.
func (r *runner) resumeComposeContainer(
	ctx context.Context,
	params *composeContainerParams,
	details *config.ContainerDetails,
) (*config.ContainerDetails, error) {
	if details == nil || params.runParams.options.Recreate {
		return nil, fmt.Errorf(
			"compose workspace has no reusable container; rerun workspace up explicitly before connecting",
		)
	}
	if _, err := verifiedComposeContract(details); err != nil {
		return nil, err
	}
	if err := r.verifyComposeIdentity(
		details, params.project.Name, params.runParams.parsedConfig.Config.Service,
	); err != nil {
		return nil, err
	}
	if details.State.Status == config.ContainerStatusRunning {
		return details, nil
	}
	return r.startComposeInstance(ctx, params, details)
}

func (r *runner) startComposeInstance(
	ctx context.Context,
	params *composeContainerParams,
	details *config.ContainerDetails,
) (*config.ContainerDetails, error) {
	args, err := composeResumeArgs(details, params.project.Name)
	if err != nil {
		return nil, err
	}
	args = r.onlyRunServices(append(args, "start"), params.runParams.parsedConfig)
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()
	if err := params.composeHelper.Run(ctx, args, nil, writer, writer); err != nil {
		return nil, fmt.Errorf("start existing compose workspace: %w", err)
	}
	started, err := params.composeHelper.FindDevContainer(
		ctx, params.project.Name, params.runParams.parsedConfig.Config.Service,
	)
	if err != nil {
		return nil, fmt.Errorf("find resumed compose container: %w", err)
	}
	if started == nil || started.ID != details.ID {
		return nil, fmt.Errorf(
			"compose workspace instance changed during restart; rerun workspace up explicitly before connecting",
		)
	}
	return started, nil
}

func composeResumeArgs(details *config.ContainerDetails, projectName string) ([]string, error) {
	files := strings.Split(details.Config.Labels[ConfigFilesLabel], ",")
	if files[0] == "" || !allProjectFilesExist(files) {
		return nil, fmt.Errorf(
			"compose workspace saved project files are unavailable; restore them or " +
				"rerun workspace up explicitly before connecting",
		)
	}
	args := []string{composeProjectNameFlag, projectName}
	persisted, found, err := persistedComposeInvocationArgs(details, projectName)
	if err != nil {
		return nil, err
	}
	if found {
		return append(args, persisted...), nil
	}
	for _, file := range files {
		args = append(args, "-f", file)
	}
	return args, nil
}
