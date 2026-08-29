package kubernetes

import (
	"testing"

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
