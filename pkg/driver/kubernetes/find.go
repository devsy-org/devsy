package kubernetes

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	corev1 "k8s.io/api/core/v1"
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
	topology := describeTopology(pod, workspaceId, containerInfo)

	return &config.ContainerDetails{
		ID:                     pvc.Name,
		Created:                pvc.CreationTimestamp.String(),
		Dockerless:             topology.dockerless,
		ImageEntrypoint:        topology.imageEntrypoint,
		LifecycleTopologyError: topology.err,
		State: config.ContainerDetailsState{
			Status:    status,
			StartedAt: startedAt,
		},
		Config: config.ContainerDetailsConfig{
			Labels: topology.labels,
		},
	}, nil
}

type topologyDescription struct {
	dockerless, imageEntrypoint bool
	err                         string
	labels                      map[string]string
}

// A volume without saved options still has to be found so stop and delete can clean it up.
func describeTopology(
	pod *corev1.Pod,
	workspaceId string,
	containerInfo *DevContainerInfo,
) topologyDescription {
	if containerInfo.Options == nil {
		return topologyDescription{
			err:    fmt.Sprintf("workspace volume %q has no saved run options", workspaceId),
			labels: map[string]string{},
		}
	}
	result := topologyDescription{labels: config.ListToObject(containerInfo.Options.Labels)}
	var topologyErr error
	result.dockerless, result.imageEntrypoint, topologyErr = observedPodTopology(
		pod, containerInfo.Options)
	if topologyErr != nil {
		result.err = topologyErr.Error()
	}
	return result
}
