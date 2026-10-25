package kubernetes

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
)

func (k *KubernetesDriver) FindDevContainer(
	ctx context.Context,
	workspaceId string,
) (*config.ContainerDetails, error) {
	log.Debugf("Finding devcontainer for workspace %q", workspaceId)
	defer log.Debugf("Done finding devcontainer for workspace %q", workspaceId)

	workspaceId = getID(workspaceId)

	pvc, containerInfo, err := k.getDevContainerPvc(ctx, workspaceId)
	if err != nil {
		return nil, err
	} else if pvc == nil {
		return nil, nil
	}

	// check pod
	pod, err := k.getPod(ctx, pvc.Name)
	if err != nil {
		return nil, fmt.Errorf("find pod for workspace %q: %w", workspaceId, err)
	}

	// determine status
	status := config.ContainerStatusExited
	if pod != nil && classifyPod(pod) == podRunning {
		status = config.ContainerStatusRunning
	}

	// check started
	startedAt := pvc.CreationTimestamp.String()
	if pod != nil {
		startedAt = pod.CreationTimestamp.String()
	}

	return &config.ContainerDetails{
		ID:      pvc.Name,
		Created: pvc.CreationTimestamp.String(),
		State: config.ContainerDetailsState{
			Status:    status,
			StartedAt: startedAt,
		},
		Config: config.ContainerDetailsConfig{
			Labels: config.ListToObject(containerInfo.Options.Labels),
		},
	}, nil
}
