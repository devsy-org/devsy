package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/types"
)

const (
	testNameChild    = "child"
	testImageUbuntu  = "ubuntu:20.04"
	testUserRoot     = "root"
	testUserVscode   = "vscode"
	testOriginParent = "/tmp/parent.json"
	testOriginChild  = "/tmp/child.json"
	testFileBase     = "base.json"
)

func writeJSON(t *testing.T, dir, filename, content string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	// #nosec G306 -- test file
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtends_BasicScalarOverride(t *testing.T) {
	tmpDir := t.TempDir()
	writeJSON(t, tmpDir, "parent.json", `{
		"name": "parent",
		"image": "ubuntu:20.04",
		"remoteUser": "root"
	}`)
	writeJSON(t, tmpDir, "child.json", `{
		"extends": "parent.json",
		"name": "child",
		"remoteUser": "vscode"
	}`)

	cfg, err := ParseDevContainerJSONFile(filepath.Join(tmpDir, "child.json"))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Name != testNameChild {
		t.Errorf("expected name 'child', got %q", cfg.Name)
	}
	if cfg.Image != testImageUbuntu {
		t.Errorf("expected image 'ubuntu:20.04', got %q", cfg.Image)
	}
	if cfg.RemoteUser != testUserVscode {
		t.Errorf("expected remoteUser 'vscode', got %q", cfg.RemoteUser)
	}
	if !cfg.Extends.IsEmpty() {
		t.Errorf("expected extends to be cleared, got %v", cfg.Extends)
	}
}

func TestExtends_MapDeepMerge_ContainerEnv(t *testing.T) {
	tmpDir := t.TempDir()
	writeJSON(t, tmpDir, "parent.json", `{
		"name": "parent",
		"containerEnv": {
			"FOO": "from_parent",
			"BAR": "from_parent"
		}
	}`)
	writeJSON(t, tmpDir, "child.json", `{
		"extends": "parent.json",
		"containerEnv": {
			"FOO": "from_child",
			"BAZ": "from_child"
		}
	}`)

	cfg, err := ParseDevContainerJSONFile(filepath.Join(tmpDir, "child.json"))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ContainerEnv["FOO"] != "from_child" {
		t.Errorf("expected FOO='from_child', got %q", cfg.ContainerEnv["FOO"])
	}
	if cfg.ContainerEnv["BAR"] != "from_parent" {
		t.Errorf("expected BAR='from_parent', got %q", cfg.ContainerEnv["BAR"])
	}
	if cfg.ContainerEnv["BAZ"] != "from_child" {
		t.Errorf("expected BAZ='from_child', got %q", cfg.ContainerEnv["BAZ"])
	}
}

func TestExtends_MapDeepMerge_Features(t *testing.T) {
	tmpDir := t.TempDir()
	writeJSON(t, tmpDir, "parent.json", `{
		"name": "parent",
		"features": {
			"ghcr.io/devcontainers/features/git:1": {},
			"ghcr.io/devcontainers/features/node:1": {"version": "16"}
		}
	}`)
	writeJSON(t, tmpDir, "child.json", `{
		"extends": "parent.json",
		"features": {
			"ghcr.io/devcontainers/features/node:1": {"version": "20"},
			"ghcr.io/devcontainers/features/python:1": {}
		}
	}`)

	cfg, err := ParseDevContainerJSONFile(filepath.Join(tmpDir, "child.json"))
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := cfg.Features["ghcr.io/devcontainers/features/git:1"]; !ok {
		t.Error("expected git feature to be preserved from parent")
	}
	if _, ok := cfg.Features["ghcr.io/devcontainers/features/python:1"]; !ok {
		t.Error("expected python feature to be added from child")
	}
	nodeFeature, ok := cfg.Features["ghcr.io/devcontainers/features/node:1"]
	if !ok {
		t.Fatal("expected node feature")
	}
	nodeMap, ok := nodeFeature.(map[string]any)
	if !ok {
		t.Fatalf("expected node feature to be map, got %T", nodeFeature)
	}
	if nodeMap["version"] != "20" {
		t.Errorf("expected node version '20', got %v", nodeMap["version"])
	}
}

func TestExtends_ArrayReplacement(t *testing.T) {
	tmpDir := t.TempDir()
	writeJSON(t, tmpDir, "parent.json", `{
		"name": "parent",
		"forwardPorts": [3000, 5000],
		"runArgs": ["--network=host"],
		"capAdd": ["SYS_PTRACE"]
	}`)
	writeJSON(t, tmpDir, "child.json", `{
		"extends": "parent.json",
		"forwardPorts": [8080],
		"capAdd": ["NET_ADMIN", "SYS_ADMIN"]
	}`)

	cfg, err := ParseDevContainerJSONFile(filepath.Join(tmpDir, "child.json"))
	if err != nil {
		t.Fatal(err)
	}

	// forwardPorts: replaced entirely
	if len(cfg.ForwardPorts) != 1 || cfg.ForwardPorts[0] != "8080" {
		t.Errorf("expected forwardPorts [8080], got %v", cfg.ForwardPorts)
	}
	// runArgs: preserved from parent (child didn't set it)
	if len(cfg.RunArgs) != 1 || cfg.RunArgs[0] != "--network=host" {
		t.Errorf("expected runArgs [--network=host], got %v", cfg.RunArgs)
	}
	// capAdd: replaced entirely
	if len(cfg.CapAdd) != 2 {
		t.Errorf("expected 2 capAdd entries, got %d", len(cfg.CapAdd))
	}
}

func TestExtends_LifecycleHookReplacement(t *testing.T) {
	tmpDir := t.TempDir()
	writeJSON(t, tmpDir, "parent.json", `{
		"name": "parent",
		"postCreateCommand": "echo parent",
		"onCreateCommand": "echo oncreate-parent"
	}`)
	writeJSON(t, tmpDir, "child.json", `{
		"extends": "parent.json",
		"postCreateCommand": "echo child"
	}`)

	cfg, err := ParseDevContainerJSONFile(filepath.Join(tmpDir, "child.json"))
	if err != nil {
		t.Fatal(err)
	}

	// postCreateCommand: child replaces
	if len(cfg.PostCreateCommand) == 0 {
		t.Fatal("expected postCreateCommand to be set")
	}
	cmds := cfg.PostCreateCommand[""]
	if len(cmds) != 1 || cmds[0] != "echo child" {
		t.Errorf("expected postCreateCommand 'echo child', got %v", cfg.PostCreateCommand)
	}

	// onCreateCommand: preserved from parent
	if len(cfg.OnCreateCommand) == 0 {
		t.Fatal("expected onCreateCommand to be preserved from parent")
	}
	oncreate := cfg.OnCreateCommand[""]
	if len(oncreate) != 1 || oncreate[0] != "echo oncreate-parent" {
		t.Errorf("expected onCreateCommand 'echo oncreate-parent', got %v", cfg.OnCreateCommand)
	}
}

func TestExtends_CycleDetection(t *testing.T) {
	tmpDir := t.TempDir()
	writeJSON(t, tmpDir, "a.json", `{
		"extends": "b.json",
		"name": "a"
	}`)
	writeJSON(t, tmpDir, "b.json", `{
		"extends": "a.json",
		"name": "b"
	}`)

	_, err := ParseDevContainerJSONFile(filepath.Join(tmpDir, "a.json"))
	if err == nil {
		t.Fatal("expected cycle detection error")
	}
	if !strings.Contains(err.Error(), "cycle detected") {
		t.Errorf("expected 'cycle detected' in error, got: %v", err)
	}
}

func TestExtends_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	writeJSON(t, tmpDir, "child.json", `{
		"extends": "nonexistent.json",
		"name": "child"
	}`)

	_, err := ParseDevContainerJSONFile(filepath.Join(tmpDir, "child.json"))
	if err == nil {
		t.Fatal("expected error for missing extends file")
	}
	if !strings.Contains(err.Error(), "nonexistent.json") {
		t.Errorf("expected error to mention missing file, got: %v", err)
	}
}

func TestExtends_MultiLevel(t *testing.T) {
	tmpDir := t.TempDir()
	writeJSON(t, tmpDir, "grandparent.json", `{
		"name": "grandparent",
		"image": "ubuntu:18.04",
		"remoteUser": "root",
		"containerEnv": {"LEVEL": "grandparent", "GP_ONLY": "gp"}
	}`)
	writeJSON(t, tmpDir, "parent.json", `{
		"extends": "grandparent.json",
		"name": "parent",
		"image": "ubuntu:20.04",
		"containerEnv": {"LEVEL": "parent", "P_ONLY": "p"}
	}`)
	writeJSON(t, tmpDir, "child.json", `{
		"extends": "parent.json",
		"name": "child",
		"containerEnv": {"LEVEL": "child"}
	}`)

	cfg, err := ParseDevContainerJSONFile(filepath.Join(tmpDir, "child.json"))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Name != testNameChild {
		t.Errorf("expected name 'child', got %q", cfg.Name)
	}
	if cfg.Image != testImageUbuntu {
		t.Errorf("expected image 'ubuntu:20.04', got %q", cfg.Image)
	}
	if cfg.RemoteUser != testUserRoot {
		t.Errorf("expected remoteUser 'root', got %q", cfg.RemoteUser)
	}
	if cfg.ContainerEnv["LEVEL"] != testNameChild {
		t.Errorf("expected LEVEL='child', got %q", cfg.ContainerEnv["LEVEL"])
	}
	if cfg.ContainerEnv["GP_ONLY"] != "gp" {
		t.Errorf("expected GP_ONLY='gp', got %q", cfg.ContainerEnv["GP_ONLY"])
	}
	if cfg.ContainerEnv["P_ONLY"] != "p" {
		t.Errorf("expected P_ONLY='p', got %q", cfg.ContainerEnv["P_ONLY"])
	}
}

func TestExtends_NoExtends(t *testing.T) {
	tmpDir := t.TempDir()
	writeJSON(t, tmpDir, "standalone.json", `{
		"name": "standalone",
		"image": "node:18"
	}`)

	cfg, err := ParseDevContainerJSONFile(filepath.Join(tmpDir, "standalone.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "standalone" {
		t.Errorf("expected name 'standalone', got %q", cfg.Name)
	}
	if cfg.Image != "node:18" {
		t.Errorf("expected image 'node:18', got %q", cfg.Image)
	}
}

func TestExtends_OriginPreserved(t *testing.T) {
	tmpDir := t.TempDir()
	writeJSON(t, tmpDir, "parent.json", `{"name": "parent", "image": "ubuntu:20.04"}`)
	childPath := writeJSON(t, tmpDir, "child.json", `{"extends": "parent.json", "name": "child"}`)

	cfg, err := ParseDevContainerJSONFile(childPath)
	if err != nil {
		t.Fatal(err)
	}

	absChild, _ := filepath.Abs(childPath)
	if cfg.Origin != absChild {
		t.Errorf("expected Origin=%q, got %q", absChild, cfg.Origin)
	}
}

func TestExtends_NestedStructBuildMerge(t *testing.T) {
	tmpDir := t.TempDir()
	writeJSON(t, tmpDir, "parent.json", `{
		"name": "parent",
		"build": {
			"dockerfile": "Dockerfile.parent",
			"args": {"BASE_IMAGE": "ubuntu:20.04", "VARIANT": "default"}
		}
	}`)
	writeJSON(t, tmpDir, "child.json", `{
		"extends": "parent.json",
		"build": {
			"dockerfile": "Dockerfile.child",
			"args": {"VARIANT": "custom", "EXTRA": "added"}
		}
	}`)

	cfg, err := ParseDevContainerJSONFile(filepath.Join(tmpDir, "child.json"))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Build == nil {
		t.Fatal("expected Build to be set")
	}
	if cfg.Build.Dockerfile != "Dockerfile.child" {
		t.Errorf("expected dockerfile 'Dockerfile.child', got %q", cfg.Build.Dockerfile)
	}
	if cfg.Build.Args["BASE_IMAGE"] != testImageUbuntu {
		t.Errorf("expected BASE_IMAGE from parent, got %q", cfg.Build.Args["BASE_IMAGE"])
	}
	if cfg.Build.Args["VARIANT"] != "custom" {
		t.Errorf("expected VARIANT='custom' from child, got %q", cfg.Build.Args["VARIANT"])
	}
	if cfg.Build.Args["EXTRA"] != "added" {
		t.Errorf("expected EXTRA='added' from child, got %q", cfg.Build.Args["EXTRA"])
	}
}

func TestMergeExtendsConfigs_Scalars(t *testing.T) {
	parent := &DevContainerConfig{
		DevContainerConfigBase: DevContainerConfigBase{
			Name:       "parent",
			RemoteUser: testUserRoot,
		},
		ImageContainer: ImageContainer{Image: testImageUbuntu},
	}
	parent.Origin = testOriginParent

	child := &DevContainerConfig{
		DevContainerConfigBase: DevContainerConfigBase{
			Name: testNameChild,
		},
	}
	child.Origin = testOriginChild

	result := mergeExtendsConfigs(parent, child)

	if result.Name != testNameChild {
		t.Errorf("Name: got %q, want 'child'", result.Name)
	}
	if result.RemoteUser != testUserRoot {
		t.Errorf("RemoteUser: got %q, want 'root'", result.RemoteUser)
	}
	if result.Image != testImageUbuntu {
		t.Errorf("Image: got %q, want 'ubuntu:20.04'", result.Image)
	}
	if result.Origin != testOriginChild {
		t.Errorf("Origin: got %q, want %q", result.Origin, testOriginChild)
	}
	if !result.Extends.IsEmpty() {
		t.Errorf("Extends: should be cleared, got %v", result.Extends)
	}
}

func TestMergeExtendsConfigs_PointerScalars(t *testing.T) {
	boolTrue := true
	boolFalse := false

	parent := &DevContainerConfig{
		DevContainerConfigBase: DevContainerConfigBase{
			UpdateRemoteUserUID: &boolTrue,
		},
		NonComposeBase: NonComposeBase{
			Init: &boolTrue,
		},
	}
	parent.Origin = testOriginParent

	child := &DevContainerConfig{
		DevContainerConfigBase: DevContainerConfigBase{
			UpdateRemoteUserUID: &boolFalse,
		},
	}
	child.Origin = testOriginChild

	result := mergeExtendsConfigs(parent, child)

	if result.UpdateRemoteUserUID == nil || *result.UpdateRemoteUserUID != false {
		t.Error("UpdateRemoteUserUID: expected false from child")
	}
	if result.Init == nil || *result.Init != true {
		t.Error("Init: expected true from parent")
	}
}

func TestMergeExtendsConfigs_Maps(t *testing.T) {
	parent := &DevContainerConfig{
		DevContainerConfigBase: DevContainerConfigBase{
			Features:  map[string]any{"feat-a": map[string]any{}},
			RemoteEnv: map[string]*string{"A": strPtr("1")},
		},
		NonComposeBase: NonComposeBase{
			ContainerEnv: map[string]string{"X": "parent"},
		},
	}
	parent.Origin = testOriginParent

	child := &DevContainerConfig{
		DevContainerConfigBase: DevContainerConfigBase{
			Features:  map[string]any{"feat-b": map[string]any{}},
			RemoteEnv: map[string]*string{"B": strPtr("2")},
		},
		NonComposeBase: NonComposeBase{
			ContainerEnv: map[string]string{"X": testNameChild, "Y": testNameChild},
		},
	}
	child.Origin = testOriginChild

	result := mergeExtendsConfigs(parent, child)

	if result.Features["feat-a"] == nil {
		t.Error("Features: expected feat-a from parent")
	}
	if result.Features["feat-b"] == nil {
		t.Error("Features: expected feat-b from child")
	}
	if result.ContainerEnv["X"] != testNameChild {
		t.Errorf("ContainerEnv X: got %q, want 'child'", result.ContainerEnv["X"])
	}
	if result.ContainerEnv["Y"] != testNameChild {
		t.Errorf("ContainerEnv Y: got %q, want 'child'", result.ContainerEnv["Y"])
	}
	if *result.RemoteEnv["A"] != "1" {
		t.Error("RemoteEnv: expected A=1 from parent")
	}
	if *result.RemoteEnv["B"] != "2" {
		t.Error("RemoteEnv: expected B=2 from child")
	}
}

func TestMergeExtendsConfigs_ArraysAndHooks(t *testing.T) {
	parent := &DevContainerConfig{
		DevContainerConfigBase: DevContainerConfigBase{
			ForwardPorts:      types.StrIntArray{"3000"},
			InitializeCommand: types.LifecycleHook{"": {"echo parent-init"}},
		},
		DevContainerActions: DevContainerActions{
			OnCreateCommand: types.LifecycleHook{"": {"echo parent-oncreate"}},
		},
	}
	parent.Origin = testOriginParent

	child := &DevContainerConfig{}
	child.Origin = testOriginChild

	result := mergeExtendsConfigs(parent, child)

	// Arrays: parent preserved when child is nil
	if len(result.ForwardPorts) != 1 || result.ForwardPorts[0] != "3000" {
		t.Errorf("ForwardPorts: expected [3000] from parent, got %v", result.ForwardPorts)
	}
	// Lifecycle: parent preserved when child is empty
	if len(result.InitializeCommand) == 0 {
		t.Error("InitializeCommand: expected parent value")
	}
	if len(result.OnCreateCommand) == 0 {
		t.Error("OnCreateCommand: expected parent value")
	}
}

func TestExtends_ArraySingleRef(t *testing.T) {
	tmpDir := t.TempDir()
	writeJSON(t, tmpDir, testFileBase, `{
		"name": "base",
		"image": "ubuntu:20.04",
		"remoteUser": "vscode"
	}`)
	childPath := writeJSON(t, tmpDir, "devcontainer.json", `{
		"extends": ["base.json"],
		"name": "child"
	}`)

	cfg, err := ParseDevContainerJSONFile(childPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != testNameChild {
		t.Errorf("expected name 'child', got %q", cfg.Name)
	}
	if cfg.Image != testImageUbuntu {
		t.Errorf("expected image 'ubuntu:20.04', got %q", cfg.Image)
	}
}

func TestExtends_ArrayMultipleRefs_Scalars(t *testing.T) {
	tmpDir := t.TempDir()
	writeJSON(t, tmpDir, testFileBase, `{
		"image": "ubuntu:20.04",
		"containerEnv": {"FROM_BASE": "base-val", "SHARED": "from-base"}
	}`)
	writeJSON(t, tmpDir, "middle.json", `{
		"remoteUser": "vscode",
		"containerEnv": {"FROM_MIDDLE": "mid-val", "SHARED": "from-middle"}
	}`)
	childPath := writeJSON(t, tmpDir, "devcontainer.json", `{
		"extends": ["base.json", "middle.json"],
		"name": "child",
		"containerEnv": {"FROM_CHILD": "child-val"}
	}`)

	cfg, err := ParseDevContainerJSONFile(childPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != testNameChild {
		t.Errorf("expected name 'child', got %q", cfg.Name)
	}
	if cfg.Image != testImageUbuntu {
		t.Errorf("expected image from base, got %q", cfg.Image)
	}
	if cfg.RemoteUser != testUserVscode {
		t.Errorf("expected remoteUser from middle, got %q", cfg.RemoteUser)
	}
}

func TestExtends_ArrayMultipleRefs_EnvMerge(t *testing.T) {
	tmpDir := t.TempDir()
	writeJSON(t, tmpDir, testFileBase, `{
		"image": "ubuntu:20.04",
		"containerEnv": {"FROM_BASE": "base-val", "SHARED": "from-base"}
	}`)
	writeJSON(t, tmpDir, "middle.json", `{
		"remoteUser": "vscode",
		"containerEnv": {"FROM_MIDDLE": "mid-val", "SHARED": "from-middle"}
	}`)
	childPath := writeJSON(t, tmpDir, "devcontainer.json", `{
		"extends": ["base.json", "middle.json"],
		"name": "child",
		"containerEnv": {"FROM_CHILD": "child-val"}
	}`)

	cfg, err := ParseDevContainerJSONFile(childPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ContainerEnv["FROM_BASE"] != "base-val" {
		t.Error("missing FROM_BASE from base")
	}
	if cfg.ContainerEnv["FROM_MIDDLE"] != "mid-val" {
		t.Error("missing FROM_MIDDLE from middle")
	}
	if cfg.ContainerEnv["FROM_CHILD"] != "child-val" {
		t.Error("missing FROM_CHILD from child")
	}
	if cfg.ContainerEnv["SHARED"] != "from-middle" {
		t.Errorf("SHARED: got %q, want from-middle", cfg.ContainerEnv["SHARED"])
	}
}

func TestExtends_ArrayOrderMatters(t *testing.T) {
	tmpDir := t.TempDir()
	writeJSON(t, tmpDir, "a.json", `{"remoteUser": "a-user", "image": "img-a"}`)
	writeJSON(t, tmpDir, "b.json", `{"remoteUser": "b-user"}`)
	childPath := writeJSON(t, tmpDir, "devcontainer.json", `{
		"extends": ["a.json", "b.json"],
		"name": "child"
	}`)

	cfg, err := ParseDevContainerJSONFile(childPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RemoteUser != "b-user" {
		t.Errorf("later ref should override: got %q, want 'b-user'", cfg.RemoteUser)
	}
	if cfg.Image != "img-a" {
		t.Errorf("image from a should remain: got %q", cfg.Image)
	}
}

func TestExtends_ArrayCycleDetection(t *testing.T) {
	tmpDir := t.TempDir()
	writeJSON(t, tmpDir, "a.json", `{"extends": "b.json", "name": "a"}`)
	writeJSON(t, tmpDir, "b.json", `{"extends": "a.json", "name": "b"}`)
	childPath := writeJSON(t, tmpDir, "devcontainer.json", `{
		"extends": ["a.json"]
	}`)

	_, err := ParseDevContainerJSONFile(childPath)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected 'cycle' in error, got: %v", err)
	}
}

func TestExtendsRef_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  ExtendsRef
	}{
		{"single string", `"base.json"`, ExtendsRef{testFileBase}},
		{"array single", `["base.json"]`, ExtendsRef{testFileBase}},
		{"array multi", `["a.json","b.json"]`, ExtendsRef{"a.json", "b.json"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got ExtendsRef
			if err := got.UnmarshalJSON([]byte(tc.input)); err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len: got %d, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d]: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestExtendsRef_MarshalJSON(t *testing.T) {
	tests := []struct {
		name  string
		input ExtendsRef
		want  string
	}{
		{"single", ExtendsRef{testFileBase}, `"base.json"`},
		{"multi", ExtendsRef{"a.json", "b.json"}, `["a.json","b.json"]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.input.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("got %q, want %q", string(got), tc.want)
			}
		})
	}
}

func TestExtends_VarSub_LocalEnv(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, ".devcontainer")
	// #nosec G301 -- test directory
	if err := os.MkdirAll(subDir, 0o750); err != nil {
		t.Fatal(err)
	}

	writeJSON(t, subDir, "parent.json", `{
		"image": "ubuntu:22.04",
		"remoteUser": "dev"
	}`)

	t.Setenv("DEVSY_TEST_EXTENDS_DIR", subDir)

	childPath := writeJSON(t, subDir, "devcontainer.json", `{
		"extends": "${localEnv:DEVSY_TEST_EXTENDS_DIR}/parent.json",
		"name": "child-with-env"
	}`)

	cfg, err := ParseDevContainerJSONFile(childPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "child-with-env" {
		t.Errorf("expected name 'child-with-env', got %q", cfg.Name)
	}
	if cfg.Image != "ubuntu:22.04" {
		t.Errorf("expected image inherited from parent, got %q", cfg.Image)
	}
	if cfg.RemoteUser != "dev" {
		t.Errorf("expected remoteUser 'dev', got %q", cfg.RemoteUser)
	}
}

func TestExtends_VarSub_LocalWorkspaceFolder(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, ".devcontainer")
	// #nosec G301 -- test directory
	if err := os.MkdirAll(subDir, 0o750); err != nil {
		t.Fatal(err)
	}

	writeJSON(t, tmpDir, "base-config.json", `{
		"image": "node:20",
		"remoteUser": "node"
	}`)

	// ${localWorkspaceFolder} should resolve to the parent of .devcontainer
	childPath := writeJSON(t, subDir, "devcontainer.json", `{
		"extends": "${localWorkspaceFolder}/base-config.json",
		"name": "workspace-ref"
	}`)

	cfg, err := ParseDevContainerJSONFile(childPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "workspace-ref" {
		t.Errorf("expected name 'workspace-ref', got %q", cfg.Name)
	}
	if cfg.Image != "node:20" {
		t.Errorf("expected image 'node:20', got %q", cfg.Image)
	}
}

func TestExtends_VarSub_MissingEnvResolvesToEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, ".devcontainer")
	// #nosec G301 -- test directory
	if err := os.MkdirAll(subDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// Ensure the env var is unset
	t.Setenv("DEVSY_TEST_NONEXISTENT_VAR", "")
	_ = os.Unsetenv("DEVSY_TEST_NONEXISTENT_VAR")

	childPath := writeJSON(t, subDir, "devcontainer.json", `{
		"extends": "${localEnv:DEVSY_TEST_NONEXISTENT_VAR}/parent.json",
		"name": "missing-env"
	}`)

	// Missing env var resolves to empty string, resulting in an invalid path
	_, err := ParseDevContainerJSONFile(childPath)
	if err == nil {
		t.Fatal("expected error when env var resolves to empty and path is invalid")
	}
}

func strPtr(s string) *string {
	return &s
}
