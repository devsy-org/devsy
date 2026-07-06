package config

import (
	"sort"
	"strings"
	"testing"
)

func TestLabelKeyConventions(t *testing.T) {
	// Docker keys use the reverse-DNS dotted form; k8s keys use prefix/name.
	docker := []string{
		DockerManagedLabel, DockerWorkspaceIDLabel, DockerResourceLabel,
		DockerVolumeRoleLabel, DockerSeededLabel,
	}
	for _, k := range docker {
		if !strings.HasPrefix(k, ReverseDomain+".") {
			t.Errorf("docker label %q should start with %q.", k, ReverseDomain)
		}
		if strings.Contains(k, "/") {
			t.Errorf("docker label %q should not contain '/'", k)
		}
	}

	k8s := []string{
		K8sCreatedLabel, K8sWorkspaceUIDLabel, K8sProjectLabel,
		K8sManagedLabel, K8sResourceLabel, K8sVolumeRoleLabel,
	}
	for _, k := range k8s {
		if !strings.HasPrefix(k, Domain+"/") {
			t.Errorf("k8s label %q should start with %q/", k, Domain)
		}
	}
}

func TestDockerVolumeLabels(t *testing.T) {
	labels := DockerVolumeLabels("ws-123", VolumeRoleWorkspace)
	want := map[string]string{
		DockerManagedLabel:     LabelValueTrue,
		DockerResourceLabel:    ResourceVolume,
		DockerWorkspaceIDLabel: "ws-123",
		DockerVolumeRoleLabel:  VolumeRoleWorkspace,
	}
	for k, v := range want {
		if labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, labels[k], v)
		}
	}
}

func TestK8sVolumeLabels(t *testing.T) {
	labels := K8sVolumeLabels("ws-123", VolumeRoleWorkspace)
	want := map[string]string{
		K8sManagedLabel:      LabelValueTrue,
		K8sResourceLabel:     ResourceVolume,
		K8sWorkspaceUIDLabel: "ws-123",
		K8sVolumeRoleLabel:   VolumeRoleWorkspace,
	}
	for k, v := range want {
		if labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, labels[k], v)
		}
	}
}

func TestLabelArgs(t *testing.T) {
	args := LabelArgs(map[string]string{
		DockerManagedLabel:  LabelValueTrue,
		DockerResourceLabel: ResourceVolume,
	})
	if len(args) != 4 {
		t.Fatalf("expected 4 args, got %d: %v", len(args), args)
	}
	var pairs []string
	for i := 0; i < len(args); i += 2 {
		if args[i] != "--label" {
			t.Errorf("arg %d = %q, want --label", i, args[i])
		}
		pairs = append(pairs, args[i+1])
	}
	sort.Strings(pairs)
	got := strings.Join(pairs, ",")
	want := DockerManagedLabel + "=true," + DockerResourceLabel + "=" + ResourceVolume
	if got != want {
		t.Errorf("pairs = %q, want %q", got, want)
	}
}
