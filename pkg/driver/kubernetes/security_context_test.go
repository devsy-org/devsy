package kubernetes

import (
	"testing"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	corev1 "k8s.io/api/core/v1"
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
	sc, err := parseSecurityContext(
		"runAsUser: 1002010000\nrunAsGroup: 1002010000\nrunAsNonRoot: true\n",
	)
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
		Privileged:   new(true),
		RunAsUser:    new(int64(0)),
		RunAsGroup:   new(int64(0)),
		RunAsNonRoot: new(bool),
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

func assertCapabilitiesUnchanged(t *testing.T, sc *corev1.SecurityContext) {
	t.Helper()
	if sc.Capabilities == nil || len(sc.Capabilities.Add) != 1 {
		t.Errorf("Capabilities not preserved: %+v", sc.Capabilities)
	}
}

func assertPrivilegedUnchanged(t *testing.T, sc *corev1.SecurityContext) {
	t.Helper()
	if sc.Privileged == nil || !*sc.Privileged {
		t.Errorf("Privileged not preserved: %v", sc.Privileged)
	}
}

func TestResolveContainerSecurityContextStrict(t *testing.T) {
	sc, err := resolveContainerSecurityContext(pkgconfig.BoolTrue, "", rootBase())
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
	assertCapabilitiesUnchanged(t, sc)
	assertPrivilegedUnchanged(t, sc)
}

func TestResolveContainerSecurityContextStrictNilBase(t *testing.T) {
	sc, err := resolveContainerSecurityContext(pkgconfig.BoolTrue, "", nil)
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
	assertCapabilitiesUnchanged(t, sc)
	assertPrivilegedUnchanged(t, sc)
}

func assertCapabilitiesDropAll(t *testing.T, sc *corev1.SecurityContext) {
	t.Helper()
	if sc.Capabilities == nil || len(sc.Capabilities.Add) != 0 || len(sc.Capabilities.Drop) != 1 ||
		sc.Capabilities.Drop[0] != "ALL" {
		t.Errorf(
			"Capabilities = %+v, want override's drop=[ALL] with no inherited Add",
			sc.Capabilities,
		)
	}
}

func TestResolveContainerSecurityContextOverrideOwnCapabilitiesWin(t *testing.T) {
	sc, err := resolveContainerSecurityContext(
		"",
		"runAsUser: 1000\nallowPrivilegeEscalation: false\ncapabilities:\n  drop: [\"ALL\"]\n",
		rootBase(),
	)
	if err != nil {
		t.Fatalf("resolveContainerSecurityContext: %v", err)
	}
	assertCapabilitiesDropAll(t, sc)
	assertPrivilegedUnchanged(t, sc)
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Errorf("AllowPrivilegeEscalation = %v, want false", sc.AllowPrivilegeEscalation)
	}
}

func TestResolveContainerSecurityContextOverrideWinsOverStrict(t *testing.T) {
	sc, err := resolveContainerSecurityContext(pkgconfig.BoolTrue, "runAsUser: 5000\n", rootBase())
	if err != nil {
		t.Fatalf("resolveContainerSecurityContext: %v", err)
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != 5000 {
		t.Errorf("RunAsUser = %v, want 5000 (override wins over strict)", sc.RunAsUser)
	}
}

func TestResolveContainerSecurityContextInvalidOverride(t *testing.T) {
	if _, err := resolveContainerSecurityContext(
		"",
		"not: valid: yaml: at: all:",
		rootBase(),
	); err == nil {
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
	sc, err := (securityContextOptions{StrictSecurity: pkgconfig.BoolTrue}).resolve()
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
	sc, err := (securityContextOptions{Capabilities: caps, Privileged: new(true)}).resolve()
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
