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
	// A volume without saved options still has to be found so stop and delete can clean it up.
	var (
		dockerless, imageEntrypoint bool
		topologyError               string
		labels                      = map[string]string{}
	)
	if containerInfo.Options == nil {
		topologyError = fmt.Sprintf("workspace volume %q has no saved run options", workspaceId)
	} else {
		var topologyErr error
		dockerless, imageEntrypoint, topologyErr = observedPodTopology(pod, containerInfo.Options)
		if topologyErr != nil {
			topologyError = topologyErr.Error()
		}
		labels = config.ListToObject(containerInfo.Options.Labels)
	}

	return &config.ContainerDetails{
		ID:                     pvc.Name,
		Created:                pvc.CreationTimestamp.String(),
		Dockerless:             dockerless,
		ImageEntrypoint:        imageEntrypoint,
		LifecycleTopologyError: topologyError,
		State: config.ContainerDetailsState{
			Status:    status,
			StartedAt: startedAt,
		},
		Config: config.ContainerDetailsConfig{
			Labels: labels,
		},
	}, nil
}
