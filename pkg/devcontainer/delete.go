package devcontainer

import (
	"context"
	"fmt"
	"strings"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
)

func (r *runner) Delete(ctx context.Context, options DeleteOptions) error {
	containerDetails, err := r.driver.FindDevContainer(ctx, r.id)
	if err != nil {
		return fmt.Errorf("find dev container: %w", err)
	}
	defer r.cleanupDeliveryVolume(ctx)
	if containerDetails == nil {
		return nil
	}

	log.Infof("deleting devcontainer: devcontainerID=%s", containerDetails.ID)
	if isDockerCompose, projectName := getDockerComposeProject(containerDetails); isDockerCompose {
		err = r.deleteDockerCompose(ctx, projectName, options.RemoveVolumes)
		if err != nil {
			return err
		}
	} else {
		if strings.ToLower(containerDetails.State.Status) == "running" {
			err = r.driver.StopDevContainer(ctx, r.id)
			if err != nil {
				return err
			}
		}

		err = r.driver.DeleteDevContainer(ctx, r.id)
		if err != nil {
			return err
		}
	}

	return nil
}

// cleanupDeliveryVolume removes the devsy-managed volumes created for this
// workspace. Best-effort: failures are logged, not returned.
func (r *runner) cleanupDeliveryVolume(ctx context.Context) {
	if err := r.newAgentDelivery().Cleanup(ctx, r.id); err != nil {
		log.Debugf("best-effort delivery volume cleanup: %v", err)
	}
}

func (r *runner) Stop(ctx context.Context) error {
	containerDetails, err := r.driver.FindDevContainer(ctx, r.id)
	if err != nil {
		return fmt.Errorf("find dev container: %w", err)
	} else if containerDetails == nil {
		return nil
	}

	if strings.ToLower(containerDetails.State.Status) != "running" {
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
