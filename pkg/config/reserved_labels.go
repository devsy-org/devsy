package config

import (
	"fmt"
	"strings"
)

const (
	OverlayStructureLabel          = "devsy.overlay.structure"
	ComposeContractLabel           = "devsy.compose.contract"
	ComposeInvocationLabel         = "devsy.compose.invocation"
	WSLgSnapshotLabel              = "devsy.wslg.snapshot"
	CreationConfigLabel            = "devsy.sh/metadata-creation-config"
	MicrosandboxUserLabel          = "devsy.sh/user"
	MicrosandboxMountLabel         = "devsy.sh/microsandbox-workspace-mount"
	MicrosandboxWorkspaceUserLabel = "devsy.sh/microsandbox-workspace-user"
)

var reservedLabels = map[string]struct{}{
	DockerUserLabel:                {},
	DockerRecoveryLabel:            {},
	OverlayStructureLabel:          {},
	ComposeContractLabel:           {},
	ComposeInvocationLabel:         {},
	WSLgSnapshotLabel:              {},
	CreationConfigLabel:            {},
	MicrosandboxUserLabel:          {},
	MicrosandboxMountLabel:         {},
	MicrosandboxWorkspaceUserLabel: {},
	DockerManagedLabel:             {},
	DockerWorkspaceIDLabel:         {},
	DockerResourceLabel:            {},
	DockerVolumeRoleLabel:          {},
	DockerSeededLabel:              {},
	K8sCreatedLabel:                {},
	K8sWorkspaceUIDLabel:           {},
	K8sManagedLabel:                {},
	K8sResourceLabel:               {},
	K8sVolumeRoleLabel:             {},
}

func ValidateUserLabels(labels []string) error {
	for _, label := range labels {
		key, _, _ := strings.Cut(label, "=")
		if _, reserved := reservedLabels[key]; reserved {
			return fmt.Errorf("label %q is reserved by Devsy; remove the user-supplied label", key)
		}
	}
	return nil
}

func ValidateCreatedLabels(expected []string, actual map[string]string) error {
	values := make(map[string]string, len(expected))
	for _, label := range expected {
		key, value, _ := strings.Cut(label, "=")
		values[key] = value
	}
	for key := range reservedLabels {
		want, expectedPresent := values[key]
		got, actualPresent := actual[key]
		if expectedPresent != actualPresent || expectedPresent && want != got {
			return fmt.Errorf(
				"label %q does not match the value Devsy set; runArgs must not set Devsy reserved labels",
				key,
			)
		}
	}
	return nil
}
