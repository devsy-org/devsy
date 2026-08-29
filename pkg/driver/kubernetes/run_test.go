package kubernetes

import (
	"testing"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/driver"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	corev1 "k8s.io/api/core/v1"
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
		nil,
		"image",
		"entrypoint",
		nil,
		nil,
		nil,
		corev1.ResourceRequirements{},
		securityContextOptions{StrictSecurity: pkgconfig.BoolTrue},
		"",
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
	k := &KubernetesDriver{
		options: &provider2.ProviderKubernetesDriverConfig{StrictSecurity: pkgconfig.BoolTrue},
	}
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
	k := &KubernetesDriver{
		options: &provider2.ProviderKubernetesDriverConfig{StrictSecurity: pkgconfig.BoolTrue},
	}
	pod := &corev1.Pod{Spec: corev1.PodSpec{HostUsers: new(true)}}

	k.finalizePodSpec(pod, "devsy-ws-1", false)

	if pod.Spec.HostUsers == nil || !*pod.Spec.HostUsers {
		t.Errorf("HostUsers = %v, want true (template value preserved)", pod.Spec.HostUsers)
	}
}

func findContainerByName(pod *corev1.Pod, name string) *corev1.Container {
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == name {
			return &pod.Spec.Containers[i]
		}
	}
	return nil
}

func assertRunAsUserAndNonRoot(t *testing.T, sc *corev1.SecurityContext, wantUID int64) {
	t.Helper()
	if sc == nil {
		t.Fatalf("SecurityContext = nil, want RunAsUser=%d", wantUID)
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != wantUID {
		t.Errorf("RunAsUser = %v, want %d", sc, wantUID)
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Errorf("RunAsNonRoot = %v, want true", sc.RunAsNonRoot)
	}
}

func TestMergeSecurityContextTemplateFieldsWinPerField(t *testing.T) {
	dst := &corev1.SecurityContext{
		RunAsUser:    new(int64(1000)),
		RunAsNonRoot: new(true),
	}
	src := &corev1.SecurityContext{
		RunAsUser:      new(int64(2000)),
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}

	merged := mergeSecurityContext(dst, src)

	if merged.RunAsUser == nil || *merged.RunAsUser != 2000 {
		t.Errorf("RunAsUser = %v, want 2000 (template field wins)", merged.RunAsUser)
	}
	if merged.RunAsNonRoot == nil || !*merged.RunAsNonRoot {
		t.Errorf(
			"RunAsNonRoot = %v, want true (kept from dst, template didn't set it)",
			merged.RunAsNonRoot,
		)
	}
	wantRuntimeDefault := merged.SeccompProfile != nil &&
		merged.SeccompProfile.Type == corev1.SeccompProfileTypeRuntimeDefault
	if !wantRuntimeDefault {
		t.Errorf(
			"SeccompProfile = %v, want RuntimeDefault (template-only field applied)",
			merged.SeccompProfile,
		)
	}
}

func TestMergeSecurityContextNilSrcKeepsDst(t *testing.T) {
	dst := &corev1.SecurityContext{RunAsUser: new(int64(1000))}
	if got := mergeSecurityContext(dst, nil); got != dst {
		t.Errorf("mergeSecurityContext(dst, nil) = %v, want dst unchanged", got)
	}
}

func TestGetContainersTemplateSecurityContextWinsUnderStrict(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: DevContainerName,
				SecurityContext: &corev1.SecurityContext{
					RunAsUser:    new(int64(5000)),
					RunAsNonRoot: new(true),
				},
			}},
		},
	}

	containers, err := getContainers(
		pod,
		"image",
		"entrypoint",
		nil,
		nil,
		nil,
		corev1.ResourceRequirements{},
		securityContextOptions{StrictSecurity: pkgconfig.BoolTrue},
		"",
	)
	if err != nil {
		t.Fatalf("getContainers: %v", err)
	}
	sc := containers[0].SecurityContext
	if sc.RunAsUser == nil || *sc.RunAsUser != 5000 {
		t.Errorf(
			"RunAsUser = %v, want 5000 (POD_MANIFEST_TEMPLATE wins over STRICT_SECURITY)",
			sc.RunAsUser,
		)
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Errorf(
			"RunAsNonRoot = %v, want true (POD_MANIFEST_TEMPLATE wins over STRICT_SECURITY)",
			sc.RunAsNonRoot,
		)
	}
}

func TestGetContainersTemplateSecurityContextWinsOverAgentSecurityContext(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:            DevContainerName,
				SecurityContext: &corev1.SecurityContext{RunAsUser: new(int64(5000))},
			}},
		},
	}

	containers, err := getContainers(
		pod, "image", "entrypoint", nil, nil, nil,
		corev1.ResourceRequirements{},
		securityContextOptions{AgentSecurityContext: "runAsUser: 1000\nrunAsNonRoot: true\n"},
		"",
	)
	if err != nil {
		t.Fatalf("getContainers: %v", err)
	}
	sc := containers[0].SecurityContext
	if sc.RunAsUser == nil || *sc.RunAsUser != 5000 {
		t.Errorf(
			"RunAsUser = %v, want 5000 (POD_MANIFEST_TEMPLATE wins over AGENT_SECURITY_CONTEXT)",
			sc.RunAsUser,
		)
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Errorf(
			"RunAsNonRoot = %v, want true (AGENT_SECURITY_CONTEXT fills the field the template left unset)",
			sc.RunAsNonRoot,
		)
	}
}

func TestAssemblePodSpecOpenShiftScenario(t *testing.T) {
	k := &KubernetesDriver{
		options: &provider2.ProviderKubernetesDriverConfig{
			StrictSecurity:       pkgconfig.BoolTrue,
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

	devsyContainer := findContainerByName(pod, DevContainerName)
	if devsyContainer == nil {
		t.Fatal("devsy container not found")
	}
	assertRunAsUserAndNonRoot(t, devsyContainer.SecurityContext, 1002010000)
}
