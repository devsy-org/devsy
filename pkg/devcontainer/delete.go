package devcontainer

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
)

func (r *runner) Delete(ctx context.Context, options DeleteOptions) error {
	details, err := r.findTeardownContainer(ctx)
	if err != nil {
		return fmt.Errorf("find dev container: %w", err)
	}
	if details != nil {
		log.Infof("deleting devcontainer: devcontainerID=%s", details.ID)
	}
	return r.buildTeardownPlan(details, options).execute(ctx)
}

func (r *runner) cleanupImportedDevContainer() error {
	if r.workspaceConfig == nil ||
		r.workspaceConfig.Workspace == nil ||
		r.workspaceConfig.Workspace.Source.LocalFolder == "" {
		return nil
	}
	return CleanupImportedDevContainers(r.localWorkspaceFolder)
}

func (r *runner) Stop(ctx context.Context) error {
	containerDetails, err := r.driver.FindDevContainer(ctx, r.id)
	if err != nil {
		return fmt.Errorf("find dev container: %w", err)
	} else if containerDetails == nil {
		return nil
	}

	if containerDetails.State.Status != config.ContainerStatusRunning {
		return nil
	}

	isCompose, projectName := getDockerComposeProject(containerDetails)
	action := r.getShutdownAction(isCompose)

	switch action {
	case config.ShutdownActionNone:
		return nil
	case config.ShutdownActionStopCompose:
		if isCompose {
			return r.stopDockerCompose(ctx, projectName)
		}
		return r.driver.StopDevContainer(ctx, r.id)
	default:
		return r.driver.StopDevContainer(ctx, r.id)
	}
}

func (r *runner) getShutdownAction(isCompose bool) string {
	if r.workspaceConfig != nil &&
		r.workspaceConfig.LastDevContainerConfig != nil &&
		r.workspaceConfig.LastDevContainerConfig.Config != nil &&
		r.workspaceConfig.LastDevContainerConfig.Config.ShutdownAction != "" {
		return r.workspaceConfig.LastDevContainerConfig.Config.ShutdownAction
	}
	if isCompose {
		return config.ShutdownActionStopCompose
	}
	return config.ShutdownActionStopContainer
}

func getDockerComposeProject(containerDetails *config.ContainerDetails) (bool, string) {
	if projectName, ok := containerDetails.Config.Labels["com.docker.compose.project"]; ok {
		return true, projectName
	}

	return false, ""
}
