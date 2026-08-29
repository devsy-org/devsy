package kubernetes

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

func TestParseSecurityContextEmpty(t *testing.T) {
	sc, err := parseSecurityContext("")
	if err != nil {
		t.Fatalf("parseSecurityContext: %v", err)
	}
	if sc != nil {
		t.Errorf("got %+v, want nil", sc)
	}
}

func TestParseSecurityContextInlineYAML(t *testing.T) {
	sc, err := parseSecurityContext("runAsUser: 1002010000\nrunAsGroup: 1002010000\nrunAsNonRoot: true\n")
	if err != nil {
		t.Fatalf("parseSecurityContext: %v", err)
	}
	if sc == nil || sc.RunAsUser == nil || *sc.RunAsUser != 1002010000 {
		t.Fatalf("got %+v, want RunAsUser=1002010000", sc)
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Fatalf("got %+v, want RunAsNonRoot=true", sc)
	}
}

func TestParseSecurityContextInvalid(t *testing.T) {
	if _, err := parseSecurityContext("not: valid: yaml: at: all:"); err == nil {
		t.Fatal("expected error for invalid inline yaml and nonexistent file path")
	}
}

func rootBase() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		Capabilities: &corev1.Capabilities{Add: []corev1.Capability{"NET_ADMIN"}},
		Privileged:   ptr.To(true),
		RunAsUser:    ptr.To(int64(0)),
		RunAsGroup:   ptr.To(int64(0)),
		RunAsNonRoot: ptr.To(false),
	}
}

func TestResolveContainerSecurityContextDefault(t *testing.T) {
	sc, err := resolveContainerSecurityContext("", "", rootBase())
	if err != nil {
		t.Fatalf("resolveContainerSecurityContext: %v", err)
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != 0 {
		t.Errorf("RunAsUser = %v, want 0", sc.RunAsUser)
	}
	if sc.Capabilities == nil || len(sc.Capabilities.Add) != 1 {
		t.Errorf("Capabilities not preserved: %+v", sc.Capabilities)
	}
	if sc.Privileged == nil || !*sc.Privileged {
		t.Errorf("Privileged not preserved: %v", sc.Privileged)
	}
}

func TestResolveContainerSecurityContextStrict(t *testing.T) {
	sc, err := resolveContainerSecurityContext("true", "", rootBase())
	if err != nil {
		t.Fatalf("resolveContainerSecurityContext: %v", err)
	}
	if sc.RunAsUser != nil {
		t.Errorf("RunAsUser = %v, want nil", sc.RunAsUser)
	}
	if sc.RunAsGroup != nil {
		t.Errorf("RunAsGroup = %v, want nil", sc.RunAsGroup)
	}
	if sc.RunAsNonRoot != nil {
		t.Errorf("RunAsNonRoot = %v, want nil", sc.RunAsNonRoot)
	}
	if sc.Capabilities == nil || len(sc.Capabilities.Add) != 1 {
		t.Errorf("Capabilities not preserved under STRICT_SECURITY: %+v", sc.Capabilities)
	}
	if sc.Privileged == nil || !*sc.Privileged {
		t.Errorf("Privileged not preserved under STRICT_SECURITY: %v", sc.Privileged)
	}
}

func TestResolveContainerSecurityContextStrictNilBase(t *testing.T) {
	sc, err := resolveContainerSecurityContext("true", "", nil)
	if err != nil {
		t.Fatalf("resolveContainerSecurityContext: %v", err)
	}
	if sc != nil {
		t.Errorf("got %+v, want nil", sc)
	}
}

func TestResolveContainerSecurityContextOverride(t *testing.T) {
	sc, err := resolveContainerSecurityContext(
		"",
		"runAsUser: 1002010000\nrunAsNonRoot: true\n",
		rootBase(),
	)
	if err != nil {
		t.Fatalf("resolveContainerSecurityContext: %v", err)
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != 1002010000 {
		t.Errorf("RunAsUser = %v, want 1002010000", sc.RunAsUser)
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Errorf("RunAsNonRoot = %v, want true", sc.RunAsNonRoot)
	}
	if sc.Capabilities == nil || len(sc.Capabilities.Add) != 1 {
		t.Errorf("Capabilities not preserved with override: %+v", sc.Capabilities)
	}
	if sc.Privileged == nil || !*sc.Privileged {
		t.Errorf("Privileged not preserved with override: %v", sc.Privileged)
	}
}

func TestResolveContainerSecurityContextOverrideWinsOverStrict(t *testing.T) {
	sc, err := resolveContainerSecurityContext("true", "runAsUser: 5000\n", rootBase())
	if err != nil {
		t.Fatalf("resolveContainerSecurityContext: %v", err)
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != 5000 {
		t.Errorf("RunAsUser = %v, want 5000 (override wins over strict)", sc.RunAsUser)
	}
}

func TestResolveContainerSecurityContextInvalidOverride(t *testing.T) {
	if _, err := resolveContainerSecurityContext("", "not: valid: yaml: at: all:", rootBase()); err == nil {
		t.Fatal("expected error for invalid AGENT_SECURITY_CONTEXT")
	}
}

func TestSecurityContextOptionsResolveDefault(t *testing.T) {
	sc, err := (securityContextOptions{}).resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != 0 {
		t.Errorf("RunAsUser = %v, want 0", sc.RunAsUser)
	}
}

func TestSecurityContextOptionsResolveStrictNoCapabilities(t *testing.T) {
	sc, err := (securityContextOptions{StrictSecurity: "true"}).resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sc == nil {
		t.Fatal("got nil, want a non-nil SecurityContext with cleared run-as fields")
	}
	if sc.RunAsUser != nil {
		t.Errorf("RunAsUser = %v, want nil", sc.RunAsUser)
	}
}

func TestSecurityContextOptionsResolvePropagatesCapabilities(t *testing.T) {
	caps := &corev1.Capabilities{Add: []corev1.Capability{"NET_ADMIN"}}
	sc, err := (securityContextOptions{Capabilities: caps, Privileged: ptr.To(true)}).resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sc.Capabilities != caps {
		t.Errorf("Capabilities = %v, want %v", sc.Capabilities, caps)
	}
	if sc.Privileged == nil || !*sc.Privileged {
		t.Errorf("Privileged = %v, want true", sc.Privileged)
	}
}

func TestSecurityContextOptionsResolveInvalidOverride(t *testing.T) {
	if _, err := (securityContextOptions{AgentSecurityContext: "not: valid: yaml: at: all:"}).resolve(); err == nil {
		t.Fatal("expected error for invalid AGENT_SECURITY_CONTEXT")
	}
}
