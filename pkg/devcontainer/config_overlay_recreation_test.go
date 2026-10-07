package devcontainer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const (
	exampleBaseImage    = "example/base:1"
	exampleUpdatedImage = "example/base:2"
	exampleCacheImage   = "example/cache:1"
	exampleNextCache    = "example/cache:2"
	composeDevService   = "dev"
	recreateFlag        = "--recreate"
)

func TestStructuralSignatureIncludesBuildAndComposeStructure(t *testing.T) {
	t.Parallel()

	baseline := &config.DevContainerConfig{Origin: overlayBaseFile}
	baseline.Image = exampleBaseImage
	baseline.DockerfileContainer = config.DockerfileContainer{
		Dockerfile: structuralDockerfile,
		Context:    "..",
		Build: &config.ConfigBuildOptions{
			Target:    "runtime",
			Args:      map[string]string{"MODE": overlayBaseHashKey},
			CacheFrom: []string{exampleCacheImage},
			Options:   []string{"--platform=linux/amd64"},
		},
	}
	baseline.ComposeContainer = config.ComposeContainer{
		DockerComposeFile: []string{structuralComposeFile},
		Service:           composeDevService,
		RunServices:       []string{"db", overlayCacheService},
	}

	baseSignature := structuralSignature(baseline)
	for _, tt := range structuralSignatureMutations() {
		t.Run(tt.name, func(t *testing.T) {
			changed := config.CloneDevContainerConfig(baseline)
			tt.mutate(changed)
			require.NotEqual(t, baseSignature, structuralSignature(changed))
		})
	}
}

func structuralSignatureMutations() []struct {
	name   string
	mutate func(*config.DevContainerConfig)
} {
	return []struct {
		name   string
		mutate func(*config.DevContainerConfig)
	}{
		{
			name:   "kind",
			mutate: func(c *config.DevContainerConfig) { c.Dockerfile = "" },
		},
		{
			name:   "image",
			mutate: func(c *config.DevContainerConfig) { c.Image = exampleUpdatedImage },
		},
		{
			name:   "dockerfile",
			mutate: func(c *config.DevContainerConfig) { c.Dockerfile = "Dockerfile.alt" },
		},
		{name: "build dockerfile fallback", mutate: func(c *config.DevContainerConfig) {
			c.Dockerfile = ""
			c.Build.Dockerfile = "Dockerfile.alt"
		}},
		{
			name:   "context",
			mutate: func(c *config.DevContainerConfig) { c.Context = "../other" },
		},
		{name: "build context fallback", mutate: func(c *config.DevContainerConfig) {
			c.Context = ""
			c.Build.Context = "../other"
		}},
		{
			name:   "build target",
			mutate: func(c *config.DevContainerConfig) { c.Build.Target = "debug" },
		},
		{
			name:   "build args",
			mutate: func(c *config.DevContainerConfig) { c.Build.Args["MODE"] = "debug" },
		},
		{
			name:   "build cache",
			mutate: func(c *config.DevContainerConfig) { c.Build.CacheFrom = []string{exampleNextCache} },
		},
		{
			name:   "build options",
			mutate: func(c *config.DevContainerConfig) { c.Build.Options = []string{"--no-cache"} },
		},
		{
			name:   "compose files",
			mutate: func(c *config.DevContainerConfig) { c.DockerComposeFile = []string{"compose.alt.yaml"} },
		},
		{name: "service", mutate: func(c *config.DevContainerConfig) { c.Service = "dev-alt" }},
		{
			name:   "run services",
			mutate: func(c *config.DevContainerConfig) { c.RunServices = []string{"db"} },
		},
	}
}

func TestStructuralSignatureIgnoresRuntimeAndLifecycleHooks(t *testing.T) {
	t.Parallel()

	base := &config.DevContainerConfig{Origin: overlayBaseFile}
	base.Image = exampleBaseImage
	baseSignature := structuralSignature(base)

	changed := config.CloneDevContainerConfig(base)
	changed.RemoteEnv = map[string]*string{"MODE": new("runtime")}
	changed.InitializeCommand = map[string][]string{"": {"echo initialize"}}
	changed.PostCreateCommand = map[string][]string{"": {"echo post-create"}}
	changed.Features = map[string]any{"example/feature": map[string]any{}}

	require.Equal(t, baseSignature, structuralSignature(changed))
}

func TestCheckOverlayRecreation(t *testing.T) {
	t.Parallel()
	for _, test := range overlayRecreationTests() {
		t.Run(test.name, func(t *testing.T) {
			runOverlayRecreationTest(t, test)
		})
	}
}

type overlayRecreationTest struct {
	name         string
	config       *config.DevContainerConfig
	details      *config.ContainerDetails
	lastConfig   *config.DevContainerConfigWithPath
	recreate     bool
	wantError    string
	wantExisting bool
}

func overlayRecreationTests() []overlayRecreationTest {
	oldImage := &config.DevContainerConfig{Origin: overlayBaseFile}
	oldImage.Image = exampleBaseImage
	newImage := config.CloneDevContainerConfig(oldImage)
	newImage.Image = exampleUpdatedImage
	oldCompose := &config.DevContainerConfig{Origin: overlayBaseFile}
	oldCompose.ComposeContainer = config.ComposeContainer{
		DockerComposeFile: []string{structuralComposeFile},
		Service:           composeDevService,
	}
	newCompose := config.CloneDevContainerConfig(oldCompose)
	newCompose.Service = "dev-alt"
	oldImageDetails := overlayContainerDetails(structuralSignature(oldImage))
	oldComposeDetails := overlayContainerDetails(structuralSignature(oldCompose))
	legacyDetails := overlayContainerDetails("")

	return []overlayRecreationTest{
		{
			name:    "same signature reuses existing container",
			config:  oldImage,
			details: oldImageDetails,
		},
		{name: "new container needs no recreate", config: newImage},
		{
			name:      "image to compose kind requires recreate",
			config:    oldCompose,
			details:   oldImageDetails,
			wantError: recreateFlag,
		},
		{
			name:      "changed compose service requires recreate",
			config:    newCompose,
			details:   oldComposeDetails,
			wantError: recreateFlag,
		},
		{
			name:         "changed structure accepts explicit recreate and tracks old container",
			config:       newCompose,
			details:      oldComposeDetails,
			recreate:     true,
			wantExisting: true,
		},
		{
			name:       "missing legacy signature falls back to matching last config",
			config:     oldImage,
			details:    legacyDetails,
			lastConfig: &config.DevContainerConfigWithPath{Config: oldImage},
		},
		{
			name:      "missing legacy state requires recreate",
			config:    oldImage,
			details:   legacyDetails,
			wantError: recreateFlag,
		},
	}
}

func runOverlayRecreationTest(t *testing.T, test overlayRecreationTest) {
	t.Helper()
	d := &overlayFindDriver{mockDriver: &mockDriver{findResult: test.details}}
	r := newTestRunner(d)
	r.workspaceConfig.LastDevContainerConfig = test.lastConfig
	parsed := &config.SubstitutedConfig{
		Config: config.CloneDevContainerConfig(test.config),
		Overlay: &config.DevContainerConfig{
			ImageContainer: config.ImageContainer{Image: "ignored-for-signature"},
		},
	}
	err := r.checkOverlayRecreation(context.Background(), parsed, UpOptions{
		CLIOptions: provider.CLIOptions{Recreate: test.recreate},
	})
	if test.wantError == "" {
		require.NoError(t, err)
	} else {
		require.ErrorContains(t, err, test.wantError)
	}
	require.Equal(t, test.wantExisting, r.overlayExisting == test.details && test.details != nil)
	require.Equal(t, 1, d.findCalls)
}

func TestCheckOverlayRecreationRejectsAttachedContainerID(t *testing.T) {
	t.Parallel()

	old := &config.DevContainerConfig{Origin: overlayBaseFile}
	old.Image = exampleBaseImage
	current := config.CloneDevContainerConfig(old)
	current.Image = exampleUpdatedImage
	current.ContainerID = "external-container"
	details := overlayContainerDetails(structuralSignature(old))
	r := newTestRunner(&mockDriver{findResult: details})

	err := r.checkOverlayRecreation(context.Background(), &config.SubstitutedConfig{
		Config: current,
		Overlay: &config.DevContainerConfig{
			ImageContainer: config.ImageContainer{Image: exampleUpdatedImage},
		},
	}, UpOptions{CLIOptions: provider.CLIOptions{Recreate: true}})

	require.ErrorContains(t, err, "attached containerID")
	require.Nil(t, r.overlayExisting)
}

func TestOverlayRecreationPreservesDriverMode(t *testing.T) {
	t.Parallel()

	for _, mode := range []driver.RecreateMode{
		driver.RecreateDelete,
		driver.RecreateStop,
		driver.RecreateOnRun,
	} {
		t.Run(string(mode), func(t *testing.T) {
			details := runningContainerDetails()
			d := &recreateDriver{
				mockDriver: mockDriver{findResult: details},
				mode:       mode,
			}
			r := newTestRunner(d)
			r.overlayExisting = details

			require.NoError(t, r.deleteForRecreate(context.Background()))
			require.Equal(t, mode == driver.RecreateDelete, d.deleteCalled)
			require.Equal(t, mode != driver.RecreateOnRun, d.stopCalled)
			require.Nil(t, r.overlayExisting)
		})
	}
}

func TestComposeTeardownArgsUsesPersistedInvocation(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	overlayPath := filepath.Join(workspace, "overlay.json")
	projectDir := filepath.Join(workspace, "compose project")
	args := []string{
		"-f",
		filepath.Join(projectDir, structuralComposeFile),
		"--env-file",
		filepath.Join(projectDir, ".env"),
		"-f",
		filepath.Join(workspace, "docker-compose.devcontainer.containerFeatures.generated.yaml"),
	}
	invocation, err := json.Marshal(
		composeInvocation{Version: 1, Directory: projectDir, Args: args},
	)
	require.NoError(t, err)
	details := overlayContainerDetails("")
	details.Config.Labels[pkgconfig.ComposeProjectLabel] = "project-name"
	details.Config.Labels[composeInvocationLabel] = string(invocation)
	d := &mockDriver{findResult: details}
	r := newTestRunner(d)
	r.workspaceConfig.CLIOptions.ExtraDevContainerPath = overlayPath
	// Cleanup must use the recorded invocation when the overlay has disappeared
	// or become malformed after the project was started.
	require.NoError(t, os.WriteFile(overlayPath, []byte("{"), 0o600))

	got, err := r.composeTeardownArgs(context.Background(), "project-name")
	require.NoError(t, err)
	require.Equal(t, append([]string{"--project-directory", projectDir}, args...), got)
}

func TestComposeTeardownArgsRejectsMalformedInvocation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
	}{
		{name: "invalid JSON", value: "{"},
		{
			name:  "unsupported version",
			value: `{"version":2,"directory":"/project","args":["-f","` + structuralComposeFile + `"]}`,
		},
		{name: "empty args", value: `{"version":1,"directory":"/project","args":[]}`},
		{name: "odd args", value: `{"version":1,"directory":"/project","args":["-f"]}`},
		{
			name:  "unsupported arg",
			value: `{"version":1,"directory":"/project","args":["--project-name","project"]}`,
		},
		{name: "empty path", value: `{"version":1,"directory":"/project","args":["-f",""]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := overlayContainerDetails("")
			details.Config.Labels[pkgconfig.ComposeProjectLabel] = "project-name"
			details.Config.Labels[composeInvocationLabel] = tt.value
			r := newTestRunner(&mockDriver{findResult: details})

			_, err := r.composeTeardownArgs(context.Background(), "project-name")
			require.Error(t, err)
		})
	}
}

func TestRecordComposeInvocationRoundTrip(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	overridePath := filepath.Join(workspace, "start-override.yaml")
	service := composeDevService
	directory := filepath.Join(workspace, "compose project")
	args := []string{
		"-f", filepath.Join(directory, "compose$base.yaml"),
		"--env-file", filepath.Join(directory, ".env with spaces"),
		"-f", overridePath,
	}
	contents := "services:\n  " + composeDevService + ":\n    labels:\n      devsy.overlay.structure: v1:test\n"
	require.NoError(t, os.WriteFile(overridePath, []byte(contents), 0o600))

	require.NoError(t, recordComposeInvocation(overridePath, service, directory, args))

	// #nosec G304 -- overridePath was created beneath t.TempDir above.
	data, err := os.ReadFile(overridePath)
	require.NoError(t, err)
	var document yaml.Node
	require.NoError(t, yaml.Unmarshal(data, &document))
	labels := findYAMLMappingValue(document.Content[0], "services")
	labels = findYAMLMappingValue(labels, service)
	labels = findYAMLMappingValue(labels, "labels")
	require.NotNil(t, labels)
	require.NotEmpty(t, labels.Content)

	var recorded string
	for i := 0; i+1 < len(labels.Content); i += 2 {
		if labels.Content[i].Value == composeInvocationLabel {
			recorded = labels.Content[i+1].Value
			break
		}
	}
	require.NotEmpty(t, recorded)
	require.Contains(t, recorded, "$$base")
	require.Equal(t, 1, strings.Count(recorded, "$$base"))
	recorded = strings.ReplaceAll(recorded, "$$", "$")
	var got composeInvocation
	require.NoError(t, json.Unmarshal([]byte(recorded), &got))
	require.Equal(t, 1, got.Version)
	require.Equal(t, directory, got.Directory)
	require.Equal(t, args, got.Args)
	require.Contains(
		t,
		got.Args,
		overridePath,
		"the persisted arguments must include the generated override itself",
	)
}

func overlayContainerDetails(signature string) *config.ContainerDetails {
	details := runningContainerDetails()
	details.Config.Labels[overlayStructureLabel] = signature
	return details
}

func findYAMLMappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

type overlayFindDriver struct {
	*mockDriver
	findCalls int
}

func (d *overlayFindDriver) FindDevContainer(
	ctx context.Context,
	id string,
) (*config.ContainerDetails, error) {
	d.findCalls++
	return d.mockDriver.FindDevContainer(ctx, id)
}

func TestRemovingStructuralOverlayStillRequiresRecreation(t *testing.T) {
	t.Parallel()
	prior := &config.DevContainerConfig{
		Origin:         overlayBaseFile,
		ImageContainer: config.ImageContainer{Image: "overlay-image"},
	}
	desired := config.CloneDevContainerConfig(prior)
	desired.Image = "primary-image"
	r := newTestRunner(&mockDriver{findResult: overlayContainerDetails(structuralSignature(prior))})
	err := r.checkOverlayRecreation(
		context.Background(),
		&config.SubstitutedConfig{Config: desired},
		UpOptions{},
	)
	require.ErrorContains(t, err, "--recreate")
	require.Nil(t, r.overlayExisting)
}
