package kubernetes

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/driver"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

func TestGetContainersDefaultRunsAsRoot(t *testing.T) {
	containers, err := getContainers(
		nil, "image", "entrypoint", nil, nil, nil,
		corev1.ResourceRequirements{}, securityContextOptions{}, "",
	)
	if err != nil {
		t.Fatalf("getContainers: %v", err)
	}
	sc := containers[0].SecurityContext
	if sc == nil || sc.RunAsUser == nil || *sc.RunAsUser != 0 {
		t.Errorf("default SecurityContext = %+v, want RunAsUser=0", sc)
	}
}

func TestGetContainersStrictSecurityClearsRunAs(t *testing.T) {
	containers, err := getContainers(
		nil, "image", "entrypoint", nil, nil, nil,
		corev1.ResourceRequirements{}, securityContextOptions{StrictSecurity: "true"}, "",
	)
	if err != nil {
		t.Fatalf("getContainers: %v", err)
	}
	sc := containers[0].SecurityContext
	if sc.RunAsUser != nil {
		t.Errorf("RunAsUser = %v, want nil under STRICT_SECURITY", sc.RunAsUser)
	}
}

func TestGetContainersAgentSecurityContextOverride(t *testing.T) {
	containers, err := getContainers(
		nil, "image", "entrypoint", nil, nil, nil,
		corev1.ResourceRequirements{},
		securityContextOptions{AgentSecurityContext: "runAsUser: 1002010000\n"},
		"",
	)
	if err != nil {
		t.Fatalf("getContainers: %v", err)
	}
	sc := containers[0].SecurityContext
	if sc.RunAsUser == nil || *sc.RunAsUser != 1002010000 {
		t.Errorf("RunAsUser = %v, want 1002010000", sc.RunAsUser)
	}
}

func TestGetContainersInvalidAgentSecurityContextErrors(t *testing.T) {
	_, err := getContainers(
		nil, "image", "entrypoint", nil, nil, nil,
		corev1.ResourceRequirements{},
		securityContextOptions{AgentSecurityContext: "not: valid: yaml: at: all:"},
		"",
	)
	if err == nil {
		t.Fatal("expected error for invalid AGENT_SECURITY_CONTEXT")
	}
}

func TestFinalizePodSpecSetsHostUsersFalseWhenStrict(t *testing.T) {
	k := &KubernetesDriver{options: &provider2.ProviderKubernetesDriverConfig{StrictSecurity: "true"}}
	pod := &corev1.Pod{}

	k.finalizePodSpec(pod, "devsy-ws-1", false)

	if pod.Spec.HostUsers == nil || *pod.Spec.HostUsers {
		t.Errorf("HostUsers = %v, want false", pod.Spec.HostUsers)
	}
}

func TestFinalizePodSpecSetsHostUsersFalseWhenAgentSecurityContextSet(t *testing.T) {
	k := &KubernetesDriver{
		options: &provider2.ProviderKubernetesDriverConfig{
			AgentSecurityContext: "runAsUser: 1000\n",
		},
	}
	pod := &corev1.Pod{}

	k.finalizePodSpec(pod, "devsy-ws-1", false)

	if pod.Spec.HostUsers == nil || *pod.Spec.HostUsers {
		t.Errorf(
			"HostUsers = %v, want false when AGENT_SECURITY_CONTEXT is set without STRICT_SECURITY",
			pod.Spec.HostUsers,
		)
	}
}

func TestFinalizePodSpecLeavesHostUsersUnsetByDefault(t *testing.T) {
	k := &KubernetesDriver{options: &provider2.ProviderKubernetesDriverConfig{}}
	pod := &corev1.Pod{}

	k.finalizePodSpec(pod, "devsy-ws-1", false)

	if pod.Spec.HostUsers != nil {
		t.Errorf(
			"HostUsers = %v, want nil (untouched) when neither STRICT_SECURITY nor AGENT_SECURITY_CONTEXT is set",
			pod.Spec.HostUsers,
		)
	}
}

func TestFinalizePodSpecRespectsTemplateHostUsers(t *testing.T) {
	k := &KubernetesDriver{options: &provider2.ProviderKubernetesDriverConfig{StrictSecurity: "true"}}
	pod := &corev1.Pod{Spec: corev1.PodSpec{HostUsers: ptr.To(true)}}

	k.finalizePodSpec(pod, "devsy-ws-1", false)

	if pod.Spec.HostUsers == nil || !*pod.Spec.HostUsers {
		t.Errorf("HostUsers = %v, want true (template value preserved)", pod.Spec.HostUsers)
	}
}

func TestAssemblePodSpecOpenShiftScenario(t *testing.T) {
	k := &KubernetesDriver{
		options: &provider2.ProviderKubernetesDriverConfig{
			StrictSecurity:       "true",
			AgentSecurityContext: "runAsUser: 1002010000\nrunAsGroup: 1002010000\nrunAsNonRoot: true\n",
		},
	}
	pod := &corev1.Pod{}

	err := k.assemblePodSpec(pod, "devsy-ws-openshift", &podSpecInputs{
		options: &driver.RunOptions{Image: "image", Entrypoint: "devsy"},
		meta:    &podMetadata{labels: map[string]string{}, nodeSelector: map[string]string{}},
	})
	if err != nil {
		t.Fatalf("assemblePodSpec: %v", err)
	}

	if pod.Spec.HostUsers == nil || *pod.Spec.HostUsers {
		t.Errorf("HostUsers = %v, want false", pod.Spec.HostUsers)
	}

	var devsyContainer *corev1.Container
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == DevContainerName {
			devsyContainer = &pod.Spec.Containers[i]
		}
	}
	if devsyContainer == nil {
		t.Fatal("devsy container not found")
	}
	sc := devsyContainer.SecurityContext
	if sc == nil || sc.RunAsUser == nil || *sc.RunAsUser != 1002010000 {
		t.Errorf("RunAsUser = %v, want 1002010000", sc)
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Errorf("RunAsNonRoot = %v, want true", sc.RunAsNonRoot)
	}
}
