package kubernetes

import (
	"testing"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

func TestBuildPersistentVolumeClaimLabels(t *testing.T) {
	k := &KubernetesDriver{
		options: &provider2.ProviderKubernetesDriverConfig{
			DiskSize: "10Gi",
		},
	}

	pvc, err := k.buildPersistentVolumeClaim("devsy-ws-123", &driver.RunOptions{
		UID: "ws-123",
		WorkspaceMount: &config.Mount{
			Type:   pkgconfig.ResourceVolume,
			Target: "/workspace",
		},
	})
	if err != nil {
		t.Fatalf("buildPersistentVolumeClaim: %v", err)
	}

	labels := pvc.Labels
	want := map[string]string{
		pkgconfig.K8sManagedLabel:      pkgconfig.LabelValueTrue,
		pkgconfig.K8sResourceLabel:     pkgconfig.ResourceVolume,
		pkgconfig.K8sWorkspaceUIDLabel: "ws-123",
		pkgconfig.K8sVolumeRoleLabel:   pkgconfig.VolumeRoleWorkspace,
		DevsyCreatedLabel:              pkgconfig.LabelValueTrue,
	}
	for k, v := range want {
		if labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, labels[k], v)
		}
	}
}
