package devcontainer

import (
	"context"
	"fmt"
	"strings"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	"github.com/devsy-org/devsy/pkg/compose"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
)

func (r *runner) runResumedDockerCompose(
	ctx context.Context,
	runParams *runContainerParams,
	helper *compose.ComposeHelper,
) (*config.Result, error) {
	params, details, err := r.composeResumeTarget(ctx, runParams, helper)
	if err != nil {
		return nil, err
	}
	_, refresh, err := r.composeSecretRuntimeOptions(details, runParams.options)
	if err != nil {
		return nil, err
	}
	if refresh {
		return nil, fmt.Errorf(
			"compose workspace requires secure runtime mount migration; rerun workspace up explicitly before connecting",
		)
	}
	started, err := r.resumeComposeContainer(ctx, params, details)
	if err != nil {
		return nil, err
	}
	return r.finalizeComposeContainer(ctx, params.runParams, params.project, started)
}

func (r *runner) composeResumeTarget(
	ctx context.Context,
	runParams *runContainerParams,
	helper *compose.ComposeHelper,
) (*composeContainerParams, *config.ContainerDetails, error) {
	details, err := r.driver.FindDevContainer(ctx, r.id)
	if err != nil {
		return nil, nil, fmt.Errorf("find existing compose workspace: %w", err)
	}
	if details == nil {
		return nil, nil, fmt.Errorf(
			"compose workspace has no reusable container; rerun workspace up explicitly before connecting",
		)
	}
	project := details.Config.Labels[pkgconfig.ComposeProjectLabel]
	service := details.Config.Labels[pkgconfig.ComposeServiceLabel]
	if project == "" || service == "" {
		return nil, nil, fmt.Errorf(
			"workspace container has no saved compose identity; rerun workspace up explicitly before connecting",
		)
	}
	// Current Compose files may name a different project. Locate the instance
	// by workspace ID, then carry its saved identity through start and setup.
	parsed := *runParams.parsedConfig
	devcontainer := *parsed.Config
	devcontainer.Service = service
	parsed.Config = &devcontainer
	resumed := *runParams
	resumed.parsedConfig = &parsed
	return &composeContainerParams{
		runParams:     &resumed,
		composeHelper: helper,
		project:       &composetypes.Project{Name: project},
	}, details, nil
}

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
	if err := checkWSLgResume(details); err != nil {
		return nil, err
	}
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
		return append(args, persisted.Args...), nil
	}
	for _, file := range files {
		args = append(args, "-f", file)
	}
	return args, nil
}
