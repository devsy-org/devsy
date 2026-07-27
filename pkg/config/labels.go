package config

const (
	Domain        = BinaryName + ".sh"
	ReverseDomain = "sh." + BinaryName

	DockerManagedLabel     = ReverseDomain + ".managed"
	DockerWorkspaceIDLabel = ReverseDomain + ".workspace-id"
	DockerResourceLabel    = ReverseDomain + ".resource"
	DockerVolumeRoleLabel  = ReverseDomain + ".volume-role"
	DockerSeededLabel      = ReverseDomain + ".seeded"
	DockerRecoveryLabel    = ReverseDomain + ".recovery"
	DockerUserLabel        = BinaryName + ".user"

	K8sCreatedLabel          = Domain + "/created"
	K8sWorkspaceLabel        = Domain + "/workspace"
	K8sWorkspaceUIDLabel     = Domain + "/workspace-uid"
	K8sProjectLabel          = Domain + "/project"
	K8sManagedLabel          = Domain + "/managed"
	K8sResourceLabel         = Domain + "/resource"
	K8sVolumeRoleLabel       = Domain + "/volume-role"
	K8sInfoAnnotation        = Domain + "/info"
	K8sLastAppliedAnnotation = Domain + "/last-applied-configuration"

	AgentExecutedAnnotation = Domain + "/agent-executed"

	LabelValueTrue    = "true"
	ResourceVolume    = "volume"
	ResourceContainer = "container"

	VolumeRoleWorkspace = "workspace"
	VolumeRoleAgent     = "agent"
	VolumeRoleFeature   = "feature"

	DevcontainerIDLabel       = "dev.containers.id"
	DevcontainerMetadataLabel = "devcontainer.metadata"

	ComposeProjectLabel     = "com.docker.compose.project"
	ComposeServiceLabel     = "com.docker.compose.service"
	ComposeConfigFilesLabel = "com.docker.compose.project.config_files"

	ClusterAutoscalerSafeToEvictAnnotation = "cluster-autoscaler.kubernetes.io/safe-to-evict"
)

func DockerVolumeLabels(workspaceID, role string) map[string]string {
	return map[string]string{
		DockerManagedLabel:     LabelValueTrue,
		DockerResourceLabel:    ResourceVolume,
		DockerWorkspaceIDLabel: workspaceID,
		DockerVolumeRoleLabel:  role,
	}
}

func K8sVolumeLabels(workspaceID, role string) map[string]string {
	return map[string]string{
		K8sManagedLabel:      LabelValueTrue,
		K8sResourceLabel:     ResourceVolume,
		K8sWorkspaceUIDLabel: workspaceID,
		K8sVolumeRoleLabel:   role,
	}
}

func LabelArgs(labels map[string]string) []string {
	args := make([]string, 0, len(labels)*2)
	for k, v := range labels {
		args = append(args, "--label", k+"="+v)
	}
	return args
}
