package kubernetes

import (
	"testing"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	corev1 "k8s.io/api/core/v1"
)

const (
	testImageName   = "image"
	testEntrypoint  = "entrypoint"
	testEnvVarName  = "FOO"
	testEnvVarValue = "bar"
)

func TestGetContainersDefaultRunsAsRoot(t *testing.T) {
	containers, err := getContainers(nil, devsyContainerInputs{
		ImageName: testImageName, Entrypoint: testEntrypoint,
		Resources: corev1.ResourceRequirements{}, Security: securityContextOptions{},
	})
	if err != nil {
		t.Fatalf("getContainers: %v", err)
	}
	sc := containers[0].SecurityContext
	if sc == nil || sc.RunAsUser == nil || *sc.RunAsUser != 0 {
		t.Errorf("default SecurityContext = %+v, want RunAsUser=0", sc)
	}
}

func TestWithAgentInstallPathEnv_AppendsWhenSet(t *testing.T) {
	envVars := withAgentInstallPathEnv(
		[]corev1.EnvVar{{Name: testEnvVarName, Value: testEnvVarValue}},
		"/home/vscode/.local/bin/devsy",
	)

	if len(envVars) != 2 {
		t.Fatalf("envVars = %+v, want 2 entries", envVars)
	}
	got := envVars[1]
	want := corev1.EnvVar{Name: pkgconfig.EnvAgentPath, Value: "/home/vscode/.local/bin/devsy"}
	if got != want {
		t.Errorf("envVars[1] = %+v, want %+v", got, want)
	}
}

func TestWithAgentInstallPathEnv_LeavesUnchangedWhenUnset(t *testing.T) {
	original := []corev1.EnvVar{{Name: testEnvVarName, Value: testEnvVarValue}}

	got := withAgentInstallPathEnv(original, "")

	if len(got) != 1 || got[0] != original[0] {
		t.Errorf("envVars = %+v, want unchanged %+v", got, original)
	}
}

func TestWithAgentInstallPathEnv_ReplacesExistingEntry(t *testing.T) {
	envVars := withAgentInstallPathEnv(
		[]corev1.EnvVar{
			{Name: testEnvVarName, Value: testEnvVarValue},
			{Name: pkgconfig.EnvAgentPath, Value: "/old/path"},
		},
		"/new/path",
	)

	if len(envVars) != 2 {
		t.Fatalf("envVars = %+v, want 2 entries", envVars)
	}
	want := corev1.EnvVar{Name: pkgconfig.EnvAgentPath, Value: "/new/path"}
	if envVars[1] != want {
		t.Errorf("envVars[1] = %+v, want %+v", envVars[1], want)
	}
}

func TestGetContainersStrictSecurityClearsRunAs(t *testing.T) {
	containers, err := getContainers(nil, devsyContainerInputs{
		ImageName:  testImageName,
		Entrypoint: testEntrypoint,
		Resources:  corev1.ResourceRequirements{},
		Security:   securityContextOptions{StrictSecurity: pkgconfig.BoolTrue},
	})
	if err != nil {
		t.Fatalf("getContainers: %v", err)
	}
	sc := containers[0].SecurityContext
	if sc.RunAsUser != nil {
		t.Errorf("RunAsUser = %v, want nil under STRICT_SECURITY", sc.RunAsUser)
	}
}

func TestGetContainersAgentSecurityContextOverride(t *testing.T) {
	containers, err := getContainers(nil, devsyContainerInputs{
		ImageName:  testImageName,
		Entrypoint: testEntrypoint,
		Resources:  corev1.ResourceRequirements{},
		Security:   securityContextOptions{AgentSecurityContext: "runAsUser: 1002010000\n"},
	})
	if err != nil {
		t.Fatalf("getContainers: %v", err)
	}
	sc := containers[0].SecurityContext
	if sc.RunAsUser == nil || *sc.RunAsUser != 1002010000 {
		t.Errorf("RunAsUser = %v, want 1002010000", sc.RunAsUser)
	}
}

func TestGetContainersInvalidAgentSecurityContextErrors(t *testing.T) {
	_, err := getContainers(nil, devsyContainerInputs{
		ImageName:  testImageName,
		Entrypoint: testEntrypoint,
		Resources:  corev1.ResourceRequirements{},
		Security:   securityContextOptions{AgentSecurityContext: "not: valid: yaml: at: all:"},
	})
	if err == nil {
		t.Fatal("expected error for invalid AGENT_SECURITY_CONTEXT")
	}
}

func TestFinalizePodSpecSetsHostUsersFalseWhenStrictSecurityEnabled(t *testing.T) {
	k := &KubernetesDriver{
		options: &provider2.ProviderKubernetesDriverConfig{
			StrictSecurity: pkgconfig.BoolTrue,
		},
	}
	pod := &corev1.Pod{}

	k.finalizePodSpec(pod, "devsy-ws-1", false)

	if pod.Spec.HostUsers == nil || *pod.Spec.HostUsers {
		t.Errorf("HostUsers = %v, want false", pod.Spec.HostUsers)
	}
}

func TestFinalizePodSpecSetsHostUsersFalseWhenUserNamespacesEnabled(t *testing.T) {
	k := &KubernetesDriver{
		options: &provider2.ProviderKubernetesDriverConfig{
			KubernetesUserNamespaces: pkgconfig.BoolTrue,
		},
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
		t.Errorf("HostUsers = %v, want false", pod.Spec.HostUsers)
	}
}

func TestFinalizePodSpecLeavesHostUsersUnsetByDefault(t *testing.T) {
	k := &KubernetesDriver{options: &provider2.ProviderKubernetesDriverConfig{}}
	pod := &corev1.Pod{}

	k.finalizePodSpec(pod, "devsy-ws-1", false)

	if pod.Spec.HostUsers != nil {
		t.Errorf(
			"HostUsers = %v, want nil (untouched) by default",
			pod.Spec.HostUsers,
		)
	}
}

func TestFinalizePodSpecRespectsTemplateHostUsers(t *testing.T) {
	k := &KubernetesDriver{
		options: &provider2.ProviderKubernetesDriverConfig{
			KubernetesUserNamespaces: pkgconfig.BoolTrue,
		},
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

	containers, err := getContainers(pod, devsyContainerInputs{
		ImageName:  testImageName,
		Entrypoint: testEntrypoint,
		Resources:  corev1.ResourceRequirements{},
		Security:   securityContextOptions{StrictSecurity: pkgconfig.BoolTrue},
	})
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

	containers, err := getContainers(pod, devsyContainerInputs{
		ImageName:  testImageName,
		Entrypoint: testEntrypoint,
		Resources:  corev1.ResourceRequirements{},
		Security: securityContextOptions{
			AgentSecurityContext: "runAsUser: 1000\nrunAsNonRoot: true\n",
		},
	})
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

func TestGetContainersDefaultModeTemplateSecurityContextWins(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:            DevContainerName,
				SecurityContext: &corev1.SecurityContext{RunAsNonRoot: new(true)},
			}},
		},
	}

	containers, err := getContainers(pod, devsyContainerInputs{
		ImageName: testImageName, Entrypoint: testEntrypoint,
		Resources: corev1.ResourceRequirements{}, Security: securityContextOptions{},
	})
	if err != nil {
		t.Fatalf("getContainers: %v", err)
	}
	sc := containers[0].SecurityContext
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Errorf(
			"RunAsNonRoot = %v, want true (POD_MANIFEST_TEMPLATE wins over the hardcoded default in default mode)",
			sc.RunAsNonRoot,
		)
	}
	if sc.RunAsUser != nil {
		t.Errorf(
			"RunAsUser = %v, want nil (runAsNonRoot=true clears the implicit root default)",
			sc.RunAsUser,
		)
	}
}

func TestGetInitContainersTemplateSecurityContextWins(t *testing.T) {
	k := &KubernetesDriver{
		options: &provider2.ProviderKubernetesDriverConfig{StrictSecurity: pkgconfig.BoolTrue},
	}
	options := &driver.RunOptions{
		Mounts: []*config.Mount{{Type: pkgconfig.ResourceVolume, Target: "/workspace"}},
	}
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{
				Name:            InitContainerName,
				SecurityContext: &corev1.SecurityContext{RunAsUser: new(int64(7000))},
			}},
		},
	}

	containers, err := k.getInitContainers(options, pod, true)
	if err != nil {
		t.Fatalf("getInitContainers: %v", err)
	}
	if len(containers) != 1 {
		t.Fatalf("got %d init containers, want 1", len(containers))
	}
	sc := containers[0].SecurityContext
	if sc.RunAsUser == nil || *sc.RunAsUser != 7000 {
		t.Errorf(
			"RunAsUser = %v, want 7000 (POD_MANIFEST_TEMPLATE wins over STRICT_SECURITY for the init container)",
			sc.RunAsUser,
		)
	}
}

func TestAssemblePodSpecOpenShiftScenario(t *testing.T) {
	k := &KubernetesDriver{
		options: &provider2.ProviderKubernetesDriverConfig{
			StrictSecurity:           pkgconfig.BoolTrue,
			AgentSecurityContext:     "runAsUser: 1002010000\nrunAsGroup: 1002010000\nrunAsNonRoot: true\n",
			KubernetesUserNamespaces: pkgconfig.BoolTrue,
		},
	}
	pod := &corev1.Pod{}

	err := k.assemblePodSpec(pod, "devsy-ws-openshift", &podSpecInputs{
		options: &driver.RunOptions{Image: testImageName, Entrypoint: "devsy"},
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

func TestBuildPodContainerInputs(t *testing.T) {
	inputs := newTestPodContainerInputs()

	t.Run("daemonConfig and capabilities", func(t *testing.T) {
		assertDaemonConfigAndCaps(t, inputs)
	})

	t.Run("mounts", func(t *testing.T) {
		assertContainerMounts(t, inputs)
	})

	t.Run("envVars", func(t *testing.T) {
		assertContainerEnvVars(t, inputs)
	})
}

func newTestPodContainerInputs() *podContainerInputs {
	k := &KubernetesDriver{
		options: &provider2.ProviderKubernetesDriverConfig{
			AgentInstallPath: "/opt/devsy/bin/devsy",
		},
	}
	options := &driver.RunOptions{
		CapAdd: []string{"SYS_PTRACE"},
		Env: map[string]string{
			"FOO":                              "bar",
			pkgconfig.EnvWorkspaceDaemonConfig: "daemon-json-data",
		},
		Mounts: []*config.Mount{
			{
				Type:   driver.MountTypeTmpfs,
				Target: "/tmp",
			},
		},
	}
	mount := &config.Mount{
		Type:   driver.MountTypeVolume,
		Source: "test-workspace-pvc",
		Target: "/workspaces/my-repo",
	}

	return k.buildPodContainerInputs(options, mount)
}

func assertDaemonConfigAndCaps(t *testing.T, inputs *podContainerInputs) {
	t.Helper()
	if inputs.daemonConfig != "daemon-json-data" {
		t.Errorf("daemonConfig = %q, want %q", inputs.daemonConfig, "daemon-json-data")
	}
	if inputs.capabilities == nil || len(inputs.capabilities.Add) != 1 ||
		inputs.capabilities.Add[0] != "SYS_PTRACE" {
		t.Errorf("capabilities = %+v, want [SYS_PTRACE]", inputs.capabilities)
	}
}

func assertContainerMounts(t *testing.T, inputs *podContainerInputs) {
	t.Helper()
	if len(inputs.volumeMounts) != 2 {
		t.Fatalf("len(volumeMounts) = %d, want 2", len(inputs.volumeMounts))
	}
	if inputs.volumeMounts[0].MountPath != "/workspaces/my-repo" {
		t.Errorf(
			"volumeMounts[0].MountPath = %q, want %q",
			inputs.volumeMounts[0].MountPath,
			"/workspaces/my-repo",
		)
	}
	if inputs.volumeMounts[1].MountPath != "/tmp" {
		t.Errorf(
			"volumeMounts[1].MountPath = %q, want %q",
			inputs.volumeMounts[1].MountPath,
			"/tmp",
		)
	}
	if len(inputs.tmpfsVolumes) != 1 {
		t.Fatalf("len(tmpfsVolumes) = %d, want 1", len(inputs.tmpfsVolumes))
	}
}

func assertContainerEnvVars(t *testing.T, inputs *podContainerInputs) {
	t.Helper()
	envMap := make(map[string]string, len(inputs.envVars))
	for _, env := range inputs.envVars {
		envMap[env.Name] = env.Value
	}

	if envMap[pkgconfig.EnvAgentPath] != "/opt/devsy/bin/devsy" {
		t.Errorf("missing or incorrect %s in envVars: %+v", pkgconfig.EnvAgentPath, inputs.envVars)
	}
	if envMap["FOO"] != "bar" {
		t.Errorf("missing or incorrect FOO in envVars: %+v", inputs.envVars)
	}
	if _, exists := envMap[pkgconfig.EnvWorkspaceDaemonConfig]; exists {
		t.Errorf("daemon config env var should have been stripped from envVars")
	}
}
