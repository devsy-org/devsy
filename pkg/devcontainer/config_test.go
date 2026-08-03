package devcontainer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/suite"
)

const testWorkspaceFolder = "/workspace"

type SubstituteTestSuite struct {
	suite.Suite
	runner *runner
}

func TestSubstituteTestSuite(t *testing.T) {
	suite.Run(t, new(SubstituteTestSuite))
}

func (s *SubstituteTestSuite) SetupTest() {
	s.runner = &runner{
		id:                   "test-id",
		localWorkspaceFolder: testWorkspaceFolder,
		workspaceConfig: &provider2.AgentWorkspaceInfo{
			Workspace: &provider2.Workspace{
				ID: "test-workspace",
			},
		},
	}
}

func (s *SubstituteTestSuite) TestSubstitute_WithoutInitEnv() {
	rawConfig := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{
			Image: "${localEnv:HOME}",
		},
	}
	options := provider2.CLIOptions{}

	result, ctx, err := s.runner.substitute(options, rawConfig)

	s.NoError(err)
	s.NotNil(result)
	s.NotNil(ctx)
	s.Equal(os.Getenv("HOME"), result.Config.Image)
	s.Equal(os.Getenv("HOME"), ctx.Env["HOME"])
}

func (s *SubstituteTestSuite) TestSubstitute_WithInitEnv() {
	rawConfig := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{
			Image: "${localEnv:CUSTOM_VAR}",
		},
	}
	options := provider2.CLIOptions{
		InitEnv: []string{"CUSTOM_VAR=custom_value"},
	}

	result, ctx, err := s.runner.substitute(options, rawConfig)

	s.NoError(err)
	s.NotNil(result)
	s.NotNil(ctx)
	s.Equal("custom_value", result.Config.Image)
	s.Equal("custom_value", ctx.Env["CUSTOM_VAR"])
}

func (s *SubstituteTestSuite) TestSubstitute_InitEnvOverridesSystemEnv() {
	s.T().Setenv("TEST_VAR", "system_value")

	rawConfig := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{
			Image: "${localEnv:TEST_VAR}",
		},
	}
	options := provider2.CLIOptions{
		InitEnv: []string{"TEST_VAR=override_value"},
	}

	result, ctx, err := s.runner.substitute(options, rawConfig)

	s.NoError(err)
	s.Equal("override_value", result.Config.Image)
	s.Equal("override_value", ctx.Env["TEST_VAR"])
}

func (s *SubstituteTestSuite) TestSubstitute_MultipleInitEnvVars() {
	rawConfig := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{
			Image: "${localEnv:REGISTRY}/${localEnv:IMAGE}:${localEnv:TAG}",
		},
	}
	options := provider2.CLIOptions{
		InitEnv: []string{
			"REGISTRY=ghcr.io",
			"IMAGE=myapp",
			"TAG=latest",
		},
	}

	result, ctx, err := s.runner.substitute(options, rawConfig)

	s.NoError(err)
	s.Equal("ghcr.io/myapp:latest", result.Config.Image)
	s.Equal("ghcr.io", ctx.Env["REGISTRY"])
	s.Equal("myapp", ctx.Env["IMAGE"])
	s.Equal("latest", ctx.Env["TAG"])
}

func (s *SubstituteTestSuite) TestSubstitute_EmptyInitEnv() {
	rawConfig := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{
			Image: "alpine:latest",
		},
	}
	options := provider2.CLIOptions{
		InitEnv: []string{},
	}

	result, _, err := s.runner.substitute(options, rawConfig)

	s.NoError(err)
	s.Equal("alpine:latest", result.Config.Image)
}

func (s *SubstituteTestSuite) TestSubstitute_InitEnvInRemoteEnv() {
	rawConfig := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{
			Image: "alpine:latest",
		},
		DevContainerConfigBase: config.DevContainerConfigBase{
			RemoteEnv: map[string]*string{
				"MY_VAR": ptr("${localEnv:CUSTOM_VAR}"),
			},
		},
	}
	options := provider2.CLIOptions{
		InitEnv: []string{"CUSTOM_VAR=test_value"},
	}

	result, ctx, err := s.runner.substitute(options, rawConfig)

	s.NoError(err)
	s.Require().NotNil(result.Config.RemoteEnv["MY_VAR"])
	s.Equal("test_value", *result.Config.RemoteEnv["MY_VAR"])
	s.Equal("test_value", ctx.Env["CUSTOM_VAR"])
}

func (s *SubstituteTestSuite) TestSubstitute_MissingVariable() {
	rawConfig := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{
			Image: "${localEnv:NONEXISTENT}",
		},
	}
	options := provider2.CLIOptions{}

	result, ctx, err := s.runner.substitute(options, rawConfig)

	s.NoError(err)
	s.Equal("", result.Config.Image)
	s.NotContains(ctx.Env, "NONEXISTENT")
}

func (s *SubstituteTestSuite) TestSubstitute_AdditionalFeatures() {
	rawConfig := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{Image: "alpine:latest"},
	}
	options := provider2.CLIOptions{
		AdditionalFeatures: `{"ghcr.io/devcontainers/features/git:1": {}}`,
	}

	result, _, err := s.runner.substitute(options, rawConfig)

	s.NoError(err)
	s.Contains(result.Config.Features, "ghcr.io/devcontainers/features/git:1")
}

func (s *SubstituteTestSuite) TestSubstitute_AdditionalFeaturesMergesWithExisting() {
	rawConfig := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{Image: "alpine:latest"},
		DevContainerConfigBase: config.DevContainerConfigBase{
			Features: map[string]any{
				"ghcr.io/devcontainers/features/node:1": map[string]any{"version": "20"},
			},
		},
	}
	options := provider2.CLIOptions{
		AdditionalFeatures: `{"ghcr.io/devcontainers/features/git:1": {}}`,
	}

	result, _, err := s.runner.substitute(options, rawConfig)

	s.NoError(err)
	s.Len(result.Config.Features, 2)
	s.Contains(result.Config.Features, "ghcr.io/devcontainers/features/node:1")
	s.Contains(result.Config.Features, "ghcr.io/devcontainers/features/git:1")
}

func (s *SubstituteTestSuite) TestSubstitute_AdditionalFeaturesOverridesExisting() {
	rawConfig := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{Image: "alpine:latest"},
		DevContainerConfigBase: config.DevContainerConfigBase{
			Features: map[string]any{
				"ghcr.io/devcontainers/features/node:1": map[string]any{"version": "18"},
			},
		},
	}
	options := provider2.CLIOptions{
		AdditionalFeatures: `{"ghcr.io/devcontainers/features/node:1": {"version": "22"}}`,
	}

	result, _, err := s.runner.substitute(options, rawConfig)

	s.NoError(err)
	s.Len(result.Config.Features, 1)
	nodeOpts, ok := result.Config.Features["ghcr.io/devcontainers/features/node:1"].(map[string]any)
	s.Require().True(ok, "expected feature options to be map[string]any")
	s.Equal("22", nodeOpts["version"])
}

func (s *SubstituteTestSuite) TestSubstitute_AdditionalFeaturesInvalidJSON() {
	rawConfig := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{Image: "alpine:latest"},
	}
	options := provider2.CLIOptions{
		AdditionalFeatures: `{invalid json`,
	}

	_, _, err := s.runner.substitute(options, rawConfig)

	s.Error(err)
	s.Contains(err.Error(), "--features")
}

func (s *SubstituteTestSuite) TestSubstitute_AdditionalFeaturesEmpty() {
	rawConfig := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{Image: "alpine:latest"},
	}
	options := provider2.CLIOptions{
		AdditionalFeatures: "",
	}

	result, _, err := s.runner.substitute(options, rawConfig)

	s.NoError(err)
	s.Nil(result.Config.Features)
}

func (s *SubstituteTestSuite) TestSubstitute_WorkspaceMountConsistencyApplied() {
	const testVal = "delegated"
	rawConfig := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{Image: "alpine:latest"},
	}
	options := provider2.CLIOptions{
		WorkspaceMountConsistency: testVal,
	}

	_, ctx, err := s.runner.substitute(options, rawConfig)

	s.NoError(err)
	s.Contains(ctx.WorkspaceMount, "consistency='"+testVal+"'")
}

func (s *SubstituteTestSuite) TestSubstitute_WorkspaceMountConsistencyReplacesExisting() {
	const testVal = "delegated"
	rawConfig := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{Image: "alpine:latest"},
		NonComposeBase: config.NonComposeBase{
			WorkspaceMount: ptr("type=bind,source=/src,target=/ws,consistency='consistent'"),
		},
	}
	options := provider2.CLIOptions{
		WorkspaceMountConsistency: testVal,
	}

	_, ctx, err := s.runner.substitute(options, rawConfig)

	s.NoError(err)
	s.Contains(ctx.WorkspaceMount, "consistency='"+testVal+"'")
	s.NotContains(ctx.WorkspaceMount, "consistency='consistent'")
}

func (s *SubstituteTestSuite) TestSubstitute_WorkspaceMountConsistencyEmpty() {
	rawConfig := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{Image: "alpine:latest"},
	}
	options := provider2.CLIOptions{
		WorkspaceMountConsistency: "",
	}

	_, ctx, err := s.runner.substitute(options, rawConfig)

	s.NoError(err)
	s.NotContains(ctx.WorkspaceMount, "consistency=delegated")
}

func (s *SubstituteTestSuite) TestSubstitute_CLIMountsAppended() {
	rawConfig := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{Image: "alpine:latest"},
	}
	options := provider2.CLIOptions{
		Mounts: []string{
			"type=bind,source=/host/data,target=/data",
			"type=volume,source=myvolume,target=/vol",
		},
	}

	substitutedConfig, _, err := s.runner.substitute(options, rawConfig)

	s.NoError(err)
	s.Require().Len(substitutedConfig.Config.Mounts, 2)
	s.Equal("bind", substitutedConfig.Config.Mounts[0].Type)
	s.Equal("/host/data", substitutedConfig.Config.Mounts[0].Source)
	s.Equal("/data", substitutedConfig.Config.Mounts[0].Target)
	s.Equal("volume", substitutedConfig.Config.Mounts[1].Type)
	s.Equal("myvolume", substitutedConfig.Config.Mounts[1].Source)
	s.Equal("/vol", substitutedConfig.Config.Mounts[1].Target)
}

func (s *SubstituteTestSuite) TestSubstitute_CLIMountsMergedWithExisting() {
	rawConfig := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{Image: "alpine:latest"},
		NonComposeBase: config.NonComposeBase{
			Mounts: []*config.Mount{
				{Type: "bind", Source: "/existing", Target: "/existing-target"},
			},
		},
	}
	options := provider2.CLIOptions{
		Mounts: []string{
			"type=bind,source=/new,target=/new-target",
		},
	}

	substitutedConfig, _, err := s.runner.substitute(options, rawConfig)

	s.NoError(err)
	s.Require().Len(substitutedConfig.Config.Mounts, 2)
	s.Equal("/existing-target", substitutedConfig.Config.Mounts[0].Target)
	s.Equal("/new-target", substitutedConfig.Config.Mounts[1].Target)
}

func (s *SubstituteTestSuite) TestSubstitute_CLIMountsEmpty() {
	rawConfig := &config.DevContainerConfig{
		ImageContainer: config.ImageContainer{Image: "alpine:latest"},
		NonComposeBase: config.NonComposeBase{
			Mounts: []*config.Mount{
				{Type: "bind", Source: "/existing", Target: "/existing-target"},
			},
		},
	}
	options := provider2.CLIOptions{}

	substitutedConfig, _, err := s.runner.substitute(options, rawConfig)

	s.NoError(err)
	s.Require().Len(substitutedConfig.Config.Mounts, 1)
	s.Equal("/existing-target", substitutedConfig.Config.Mounts[0].Target)
}

func ptr(s string) *string { return new(s) }

func TestWorkspaceMountFolderWarning(t *testing.T) {
	tests := []struct {
		name    string
		conf    *config.DevContainerConfig
		wantMsg bool
	}{
		{name: "nil config", conf: nil, wantMsg: false},
		{
			name:    "neither set",
			conf:    &config.DevContainerConfig{},
			wantMsg: false,
		},
		{
			name: "both set",
			conf: &config.DevContainerConfig{
				DevContainerConfigBase: config.DevContainerConfigBase{
					WorkspaceFolder: testWorkspaceFolder,
				},
				NonComposeBase: config.NonComposeBase{WorkspaceMount: new("source=v")},
			},
			wantMsg: false,
		},
		{
			name: "mount without folder",
			conf: &config.DevContainerConfig{
				NonComposeBase: config.NonComposeBase{WorkspaceMount: new("source=v")},
			},
			wantMsg: true,
		},
		{
			name: "folder without mount",
			conf: &config.DevContainerConfig{
				DevContainerConfigBase: config.DevContainerConfigBase{
					WorkspaceFolder: testWorkspaceFolder,
				},
			},
			wantMsg: true,
		},
		{
			name: "empty-string mount satisfies the pairing",
			conf: &config.DevContainerConfig{
				DevContainerConfigBase: config.DevContainerConfigBase{
					WorkspaceFolder: testWorkspaceFolder,
				},
				NonComposeBase: config.NonComposeBase{WorkspaceMount: new("")},
			},
			wantMsg: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := workspaceMountFolderWarning(tt.conf)
			if (got != "") != tt.wantMsg {
				t.Errorf("workspaceMountFolderWarning() = %q, wantMsg=%v", got, tt.wantMsg)
			}
		})
	}
}

func newRunnerAt(folder string) *runner {
	return &runner{
		id:                   "test-id",
		localWorkspaceFolder: folder,
		workspaceConfig: &provider2.AgentWorkspaceInfo{
			Workspace: &provider2.Workspace{ID: "test-workspace"},
		},
	}
}

func seedAmbiguousProfiles(t *testing.T, folder string) {
	t.Helper()
	for _, id := range []string{"claude", "default"} {
		dir := filepath.Join(folder, ".devcontainer", id)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		body := `{"image":"repo/image","features":{"ghcr.io/x/y:1":{}}}`
		file := filepath.Join(dir, "devcontainer.json")
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGetRawConfig_SourceImageBypassesDiscovery(t *testing.T) {
	folder := t.TempDir()
	seedAmbiguousProfiles(t, folder)
	r := newRunnerAt(folder)

	const image = "python"
	conf, err := r.getRawConfig(provider2.CLIOptions{DevContainerSource: "image:" + image})
	if err != nil {
		t.Fatalf("getRawConfig: %v", err)
	}
	if conf.Image != image {
		t.Errorf("Image = %q, want %q", conf.Image, image)
	}
	if len(conf.Features) != 0 {
		t.Errorf("Features = %v, want none (project features must be ignored)", conf.Features)
	}
}

func TestGetRawConfig_SourceImageCarriesRunArgs(t *testing.T) {
	folder := t.TempDir()
	r := newRunnerAt(folder)

	conf, err := r.getRawConfig(provider2.CLIOptions{
		DevContainerSource: testImgSrc,
		RunArgs:            []string{"--add-host=host.docker.internal:host-gateway"},
	})
	if err != nil {
		t.Fatalf("getRawConfig: %v", err)
	}
	if len(conf.RunArgs) != 1 || conf.RunArgs[0] != "--add-host=host.docker.internal:host-gateway" {
		t.Errorf(
			"RunArgs = %v, want [--add-host=host.docker.internal:host-gateway] "+
				"(snapshot restore relies on this to reach a host.docker.internal-only registry)",
			conf.RunArgs,
		)
	}
}

func TestGetRawConfig_SourceImageCarriesContainerEnv(t *testing.T) {
	folder := t.TempDir()
	r := newRunnerAt(folder)

	conf, err := r.getRawConfig(provider2.CLIOptions{
		DevContainerSource: testImgSrc,
		ContainerEnv:       map[string]string{"DEVSY_INSECURE_DOCKER_INTERNAL": stringTrue},
	})
	if err != nil {
		t.Fatalf("getRawConfig: %v", err)
	}
	if conf.ContainerEnv["DEVSY_INSECURE_DOCKER_INTERNAL"] != stringTrue {
		t.Errorf(
			"ContainerEnv = %v, want DEVSY_INSECURE_DOCKER_INTERNAL=true "+
				"(snapshot restore relies on this to reach an insecure registry from inside the container)",
			conf.ContainerEnv,
		)
	}
}

func TestGetRawConfig_PersistedSourceImageBypassesDiscovery(t *testing.T) {
	folder := t.TempDir()
	seedAmbiguousProfiles(t, folder)
	r := newRunnerAt(folder)
	// Simulate a restart: the CLI option is absent, but the override was
	// persisted on the workspace during the first up.
	const image = "python"
	r.workspaceConfig.Workspace.DevContainerSource = "image:" + image

	conf, err := r.getRawConfig(provider2.CLIOptions{})
	if err != nil {
		t.Fatalf("getRawConfig: %v", err)
	}
	if conf.Image != image {
		t.Errorf("Image = %q, want %q", conf.Image, image)
	}
	if len(conf.Features) != 0 {
		t.Errorf("Features = %v, want none (project features must be ignored)", conf.Features)
	}
}

func TestGetRawConfig_CLISourceOverridesPersisted(t *testing.T) {
	folder := t.TempDir()
	r := newRunnerAt(folder)
	r.workspaceConfig.Workspace.DevContainerSource = "image:persisted"

	conf, err := r.getRawConfig(provider2.CLIOptions{DevContainerSource: "image:cli"})
	if err != nil {
		t.Fatalf("getRawConfig: %v", err)
	}
	if conf.Image != "cli" {
		t.Errorf("Image = %q, want %q (CLI option must win over persisted)", conf.Image, "cli")
	}
}

func TestGetRawConfig_SourceNoneWithImageBypassesDiscovery(t *testing.T) {
	folder := t.TempDir()
	seedAmbiguousProfiles(t, folder)
	r := newRunnerAt(folder)

	conf, err := r.getRawConfig(provider2.CLIOptions{
		DevContainerSource: string(SourceNone),
		FallbackImage:      "ubuntu",
	})
	if err != nil {
		t.Fatalf("getRawConfig: %v", err)
	}
	if len(conf.Features) != 0 {
		t.Errorf("Features = %v, want none", conf.Features)
	}
}

func TestGetRawConfig_InvalidSource(t *testing.T) {
	r := newRunnerAt(t.TempDir())
	if _, err := r.getRawConfig(provider2.CLIOptions{DevContainerSource: "bogus"}); err == nil {
		t.Fatal("expected error for invalid source spec")
	}
}

func TestGetRawConfig_SourcePreservesRootDevcontainerJSON(t *testing.T) {
	folder := t.TempDir()
	rootConfig := filepath.Join(folder, ".devcontainer.json")
	original := []byte(`{"image":"user/original","features":{"ghcr.io/x/y:1":{}}}`)
	if err := os.WriteFile(rootConfig, original, 0o600); err != nil {
		t.Fatal(err)
	}
	r := newRunnerAt(folder)

	opts := provider2.CLIOptions{DevContainerSource: testImgSrc}
	if _, err := r.getRawConfig(opts); err != nil {
		t.Fatalf("getRawConfig: %v", err)
	}

	got, err := os.ReadFile(rootConfig) //nolint:gosec // G304 — test temp file
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Errorf("root .devcontainer.json was modified:\n got:  %s\n want: %s", got, original)
	}
}
