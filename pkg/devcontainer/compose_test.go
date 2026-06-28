package devcontainer

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	"github.com/devsy-org/devsy/pkg/compose"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/feature"
	"github.com/stretchr/testify/suite"
)

type composeBuildImageNameTestCase struct {
	name          string
	composeHelper *compose.ComposeHelper
	projectName   string
	service       *composetypes.ServiceConfig
	hasFeatures   bool
	want          string
}

var composeBuildImageNameTests = []composeBuildImageNameTestCase{
	{
		name:          "keeps original image without features",
		composeHelper: &compose.ComposeHelper{Version: "2.30.0"},
		projectName:   "workspace",
		service: &composetypes.ServiceConfig{
			Name:  "app",
			Image: "ghcr.io/example/shared-base:latest",
		},
		want: "ghcr.io/example/shared-base:latest",
	},
	{
		name:          "uses workspace image for image backed features",
		composeHelper: &compose.ComposeHelper{Version: "2.30.0"},
		projectName:   "workspace",
		service: &composetypes.ServiceConfig{
			Name:  "app",
			Image: "ghcr.io/example/shared-base:latest",
		},
		hasFeatures: true,
		want:        "workspace-app",
	},
	{
		name:          "uses compose version separator for image backed features",
		composeHelper: &compose.ComposeHelper{Version: "2.7.0"},
		projectName:   "workspace",
		service: &composetypes.ServiceConfig{
			Name:  "app",
			Image: "ghcr.io/example/shared-base:latest",
		},
		hasFeatures: true,
		want:        "workspace_app",
	},
	{
		// Documents current behavior: when both image and build are set, the
		// declared image tag is used even with features, which could collide
		// with the upstream registry tag. Changing this would be intentional.
		name:          "preserves build backed services with features",
		composeHelper: &compose.ComposeHelper{Version: "2.30.0"},
		projectName:   "workspace",
		service: &composetypes.ServiceConfig{
			Name:  "app",
			Image: "ghcr.io/example/shared-base:latest",
			Build: &composetypes.BuildConfig{Context: "."},
		},
		hasFeatures: true,
		want:        "ghcr.io/example/shared-base:latest",
	},
	{
		name:          "preserves build backed services without features",
		composeHelper: &compose.ComposeHelper{Version: "2.30.0"},
		projectName:   "workspace",
		service: &composetypes.ServiceConfig{
			Name:  "app",
			Image: "ghcr.io/example/shared-base:latest",
			Build: &composetypes.BuildConfig{Context: "."},
		},
		hasFeatures: false,
		want:        "ghcr.io/example/shared-base:latest",
	},
	{
		name:          "uses default image when compose image is empty",
		composeHelper: &compose.ComposeHelper{Version: "2.30.0"},
		projectName:   "workspace",
		service: &composetypes.ServiceConfig{
			Name: "app",
		},
		hasFeatures: true,
		want:        "workspace-app",
	},
}

type ComposeSuite struct {
	suite.Suite
}

func (s *ComposeSuite) TestStripDigestFromImageRef() {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "digest reference",
			input: "registry.example.com/app:1.2.3@sha256:abcdef",
			want:  "registry.example.com/app:1.2.3",
		},
		{
			name:  "no digest",
			input: "registry.example.com/app:1.2.3",
			want:  "registry.example.com/app:1.2.3",
		},
		{
			name:  "digest without tag",
			input: "registry.example.com/app@sha256:abcdef",
			want:  "registry.example.com/app",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			got := stripDigestFromImageRef(tt.input)
			s.Equal(tt.want, got)
		})
	}
}

func (s *ComposeSuite) TestComposeBuildImageName() {
	for _, tt := range composeBuildImageNameTests {
		s.Run(tt.name, func() {
			got, err := composeBuildImageName(
				tt.composeHelper,
				tt.projectName,
				tt.service,
				tt.hasFeatures,
			)
			s.Require().NoError(err)
			s.Equal(tt.want, got)
		})
	}
}

func (s *ComposeSuite) TestCreateComposeServiceUsesBuildImageName() {
	r := &runner{}
	service := r.createComposeService(&composeServiceParams{
		composeService: &composetypes.ServiceConfig{
			Name:  "app",
			Image: "ghcr.io/example/shared-base:latest",
			Build: &composetypes.BuildConfig{Target: "original-target"},
		},
		buildImageName:          "workspace-app:latest",
		dockerfilePathInContext: "Dockerfile-with-features",
		buildContext:            "/tmp/context",
		featuresBuildInfo: &feature.BuildInfo{
			OverrideTarget: "dev_containers_target_stage",
			BuildArgs: map[string]string{
				"FEATURE_FLAG": "true",
			},
		},
	})

	s.Equal("workspace-app:latest", service.Image)
	s.Require().NotNil(service.Build)
	s.Equal("dev_containers_target_stage", service.Build.Target)
	s.Equal("Dockerfile-with-features", service.Build.Dockerfile)
	s.Equal("/tmp/context", service.Build.Context)
	s.Require().NotNil(service.Build.Args)
	s.requireBuildArgValue(service.Build.Args, "FEATURE_FLAG", "true")
	s.requireBuildArgValue(service.Build.Args, "BUILDKIT_INLINE_CACHE", "1")
}

func (s *ComposeSuite) requireBuildArgValue(
	args composetypes.MappingWithEquals,
	key, want string,
) {
	s.T().Helper()

	s.Require().NotNil(args[key])
	s.Equal(want, *args[key])
}

func TestComposeSuite(t *testing.T) {
	suite.Run(t, new(ComposeSuite))
}

type PrepareBuildContextSuite struct {
	suite.Suite
	runner *runner
}

func (s *PrepareBuildContextSuite) SetupTest() {
	s.runner = &runner{}
}

func (s *PrepareBuildContextSuite) TestNoContextRelativePath() {
	result, err := s.runner.prepareBuildContext(
		&composetypes.ServiceConfig{Name: "test-service"},
		"/tmp/features/Dockerfile",
		"FROM alpine",
		&feature.BuildInfo{FeaturesFolder: "/tmp/features/folder"},
	)

	s.NoError(err)
	s.False(
		filepath.IsAbs(result.dockerfilePathInContext),
		"dockerfilePathInContext should be relative",
	)
	s.Equal("Dockerfile", result.dockerfilePathInContext)
	s.Equal("/tmp/features", result.context)
}

func (s *PrepareBuildContextSuite) TestNilBuildRelativePath() {
	result, err := s.runner.prepareBuildContext(
		&composetypes.ServiceConfig{Name: "test-service", Build: nil},
		"/workspace/.devcontainer/features/Dockerfile",
		"FROM alpine",
		&feature.BuildInfo{FeaturesFolder: "/workspace/.devcontainer/features/folder"},
	)

	s.NoError(err)
	s.False(
		filepath.IsAbs(result.dockerfilePathInContext),
		"dockerfilePathInContext should be relative",
	)
	s.Equal("Dockerfile", result.dockerfilePathInContext)
	s.Equal("/workspace/.devcontainer/features", result.context)
}

func (s *PrepareBuildContextSuite) TestCustomBuildContext() {
	dockerfileContent := "FROM alpine\nCOPY ./" + config.DevsyContextFeatureFolder + "/ /tmp/build-features/"

	result, err := s.runner.prepareBuildContext(
		&composetypes.ServiceConfig{
			Name: "test-service",
			Build: &composetypes.BuildConfig{
				Context: "/workspace",
			},
		},
		"/workspace/.devcontainer/features/Dockerfile",
		dockerfileContent,
		&feature.BuildInfo{FeaturesFolder: "/workspace/.devcontainer/features/folder"},
	)

	s.NoError(err)
	s.False(
		filepath.IsAbs(result.dockerfilePathInContext),
		"dockerfilePathInContext should be relative",
	)
	s.Equal(".devcontainer/features/Dockerfile", result.dockerfilePathInContext)
	s.Equal("/workspace", result.context)
	s.Contains(result.dockerfileContent, "COPY ./.devcontainer/features/folder/")
	s.NotContains(result.dockerfileContent, "COPY ./"+config.DevsyContextFeatureFolder+"/")
}

func (s *PrepareBuildContextSuite) TestCustomBuildContextPreservesWhitespace() {
	dockerfileContent := "COPY  ./" + config.DevsyContextFeatureFolder + "/ /tmp/\n" +
		"ADD\t./" + config.DevsyContextFeatureFolder + "/ /other/"

	result, err := s.runner.prepareBuildContext(
		&composetypes.ServiceConfig{
			Name:  "test-service",
			Build: &composetypes.BuildConfig{Context: "/workspace"},
		},
		"/workspace/.devcontainer/features/Dockerfile",
		dockerfileContent,
		&feature.BuildInfo{FeaturesFolder: "/workspace/.devcontainer/features/folder"},
	)

	s.NoError(err)
	s.Contains(result.dockerfileContent, "COPY  ./.devcontainer/features/folder/")
	s.Contains(result.dockerfileContent, "ADD\t./.devcontainer/features/folder/")
}

func (s *PrepareBuildContextSuite) TestCustomBuildContextNoReplacementNeeded() {
	dockerfileContent := "FROM alpine\nRUN echo hello"

	result, err := s.runner.prepareBuildContext(
		&composetypes.ServiceConfig{
			Name:  "test-service",
			Build: &composetypes.BuildConfig{Context: "/workspace"},
		},
		"/workspace/.devcontainer/features/Dockerfile",
		dockerfileContent,
		&feature.BuildInfo{FeaturesFolder: "/workspace/.devcontainer/features/folder"},
	)

	s.NoError(err)
	s.Equal(dockerfileContent, result.dockerfileContent, "content should be unchanged")
}

func (s *PrepareBuildContextSuite) TestCustomBuildContextEmptyContext() {
	result, err := s.runner.prepareBuildContext(
		&composetypes.ServiceConfig{
			Name:  "test-service",
			Build: &composetypes.BuildConfig{Context: ""},
		},
		"/workspace/.devcontainer/features/Dockerfile",
		"FROM alpine",
		&feature.BuildInfo{FeaturesFolder: "/workspace/.devcontainer/features/folder"},
	)

	s.NoError(err)
	s.Equal("Dockerfile", result.dockerfilePathInContext)
	s.Equal("/workspace/.devcontainer/features", result.context)
}

func TestPrepareBuildContextSuite(t *testing.T) {
	suite.Run(t, new(PrepareBuildContextSuite))
}

func TestValidateRunServices(t *testing.T) {
	project := &composetypes.Project{
		Services: map[string]composetypes.ServiceConfig{
			"app": {Name: "app"},
			"db":  {Name: "db"},
		},
	}
	emptyProject := &composetypes.Project{Services: map[string]composetypes.ServiceConfig{}}

	tests := []struct {
		name        string
		runServices []string
		project     *composetypes.Project
		wantErr     bool
		errContains string
	}{
		{"empty runServices returns nil", nil, project, false, ""},
		{"valid services returns nil", []string{"app", "db"}, project, false, ""},
		{"invalid service returns error", []string{"nonexistent"}, project, true, "nonexistent"},
		{"mix of valid and invalid", []string{"app", "typo-svc", "bad"}, project, true, "typo-svc"},
		{"project with no services", []string{"app"}, emptyProject, true, "app"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRunServices(tt.runServices, tt.project)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errContains)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestMountToServiceVolumeConfigScalars(t *testing.T) {
	got := mountToServiceVolumeConfig(&config.Mount{
		Type: mountTypeBind, Source: "/s", Target: "/t",
		Other: []string{"readonly", "consistency=cached"},
	})
	if got.Type != mountTypeBind || got.Source != "/s" || got.Target != "/t" {
		t.Errorf("scalars wrong: got %+v", got)
	}
	if !got.ReadOnly {
		t.Error("ReadOnly should be true")
	}
	if got.Consistency != "cached" {
		t.Errorf("Consistency = %q, want cached", got.Consistency)
	}
}

func TestBindOptionsFromMount(t *testing.T) {
	tests := []struct {
		name string
		in   *config.Mount
		want *composetypes.ServiceVolumeBind
	}{
		{
			name: "no bind options",
			in:   &config.Mount{Type: mountTypeBind},
			want: nil,
		},
		{
			name: "propagation",
			in:   &config.Mount{Other: []string{"bind-propagation=rslave"}},
			want: &composetypes.ServiceVolumeBind{Propagation: "rslave"},
		},
		{
			name: "nonrecursive",
			in:   &config.Mount{Other: []string{"bind-nonrecursive"}},
			want: &composetypes.ServiceVolumeBind{Recursive: "disabled"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bindOptionsFromMount(tt.in)
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("nil-ness differs: got %+v, want %+v", got, tt.want)
			}
			if got != nil &&
				(got.Propagation != tt.want.Propagation || got.Recursive != tt.want.Recursive) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestVolumeOptionsFromMount(t *testing.T) {
	tests := []struct {
		name string
		in   *config.Mount
		want *composetypes.ServiceVolumeVolume
	}{
		{
			name: "no volume options",
			in:   &config.Mount{Type: mountTypeVolume},
			want: nil,
		},
		{
			name: "nocopy and subpath",
			in:   &config.Mount{Other: []string{"volume-nocopy", "volume-subpath=inner"}},
			want: &composetypes.ServiceVolumeVolume{NoCopy: true, Subpath: "inner"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := volumeOptionsFromMount(tt.in)
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("nil-ness differs: got %+v, want %+v", got, tt.want)
			}
			if got != nil && (got.NoCopy != tt.want.NoCopy || got.Subpath != tt.want.Subpath) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestTmpfsOptionsFromMount(t *testing.T) {
	tests := []struct {
		name string
		in   *config.Mount
		want *composetypes.ServiceVolumeTmpfs
	}{
		{
			name: "no tmpfs options",
			in:   &config.Mount{Type: "tmpfs"},
			want: nil,
		},
		{
			name: "size and mode",
			in:   &config.Mount{Other: []string{"tmpfs-size=1048576", "tmpfs-mode=1777"}},
			want: &composetypes.ServiceVolumeTmpfs{
				Size: composetypes.UnitBytes(1048576),
				Mode: 0o1777,
			},
		},
		{
			name: "invalid values dropped",
			in:   &config.Mount{Other: []string{"tmpfs-size=oops", "tmpfs-mode=oops"}},
			want: nil,
		},
		{
			name: "negative size dropped",
			in:   &config.Mount{Other: []string{"tmpfs-size=-1"}},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tmpfsOptionsFromMount(tt.in)
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("nil-ness differs: got %+v, want %+v", got, tt.want)
			}
			if got != nil && (got.Size != tt.want.Size || got.Mode != tt.want.Mode) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestEscapeComposeLabelValue(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain value untouched", in: "plain-value", want: "plain-value"},
		{name: "empty value", in: "", want: ""},

		// "$" is the only character Compose interpolates, so it is doubled.
		{name: "single dollar doubled", in: "$HOME", want: "$$HOME"},
		{name: "leading dollar", in: "$", want: "$$"},
		{name: "trailing dollar", in: "a$", want: "a$$"},
		{name: "multiple dollars", in: "$a$b$", want: "$$a$$b$$"},
		{name: "already doubled dollar is doubled again", in: "$$", want: "$$$$"},
		{name: "braced variable reference", in: "${FOO}", want: "$${FOO}"},

		// Characters that are NOT special to Compose interpolation must pass
		// through verbatim. Escaping them would corrupt the stored payload.
		{name: "single quote preserved", in: "it's", want: "it's"},
		{name: "double quote preserved", in: `say "hi"`, want: `say "hi"`},
		{name: "backslash preserved", in: `a\b`, want: `a\b`},
		{name: "backtick preserved", in: "a`b", want: "a`b"},
		{name: "newline preserved", in: "line1\nline2", want: "line1\nline2"},
		{name: "unicode preserved", in: "café—naïve 🚀", want: "café—naïve 🚀"},

		// Realistic metadata: JSON containing an apostrophe must survive so it
		// can be json.Unmarshal'd back on read. Only "$" is altered.
		{
			name: "json metadata with apostrophe and dollar",
			in:   `[{"id":"it's-a-feature","cmd":"echo $X"}]`,
			want: `[{"id":"it's-a-feature","cmd":"echo $$X"}]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapeComposeLabelValue(tt.in); got != tt.want {
				t.Errorf("escapeComposeLabelValue(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestEscapeComposeLabelValueRoundTrip documents the contract that motivates the
// escaping: doubling "$" survives Compose's interpolation (which un-doubles
// "$$" back to "$"), and non-"$" characters are returned unchanged so a
// JSON-encoded metadata label can be unmarshalled back without corruption.
func TestEscapeComposeLabelValueRoundTrip(t *testing.T) {
	original := `{"name":"it's a test","entrypoint":"run $CMD"}`

	escaped := escapeComposeLabelValue(original)

	// Compose interpolation collapses "$$" -> "$"; emulate that to recover the
	// value a consumer would see.
	interpolated := strings.ReplaceAll(escaped, "$$", "$")
	if interpolated != original {
		t.Errorf(
			"round-trip mismatch:\n  escaped      = %q\n  interpolated = %q\n  original     = %q",
			escaped,
			interpolated,
			original,
		)
	}

	// The apostrophe (and every non-"$" byte) must be untouched in the escaped
	// form so json.Unmarshal succeeds after interpolation.
	if strings.Contains(escaped, `\'`) {
		t.Errorf("apostrophe was escaped, corrupting the payload: %q", escaped)
	}
}

func TestBuildServiceLabels(t *testing.T) {
	t.Run("uses default ID label when no ID labels", func(t *testing.T) {
		r := &runner{}
		r.ID = "workspace-id"

		labels := r.buildServiceLabels(nil)

		if labels[config.DockerIDLabel] != "workspace-id" {
			t.Errorf("default ID label = %q, want %q", labels[config.DockerIDLabel], "workspace-id")
		}
	})

	t.Run("escapes dollars but preserves other characters", func(t *testing.T) {
		r := &runner{}
		r.IDLabels = []string{"id.label=$value"}

		labels := r.buildServiceLabels(map[string]string{"extra": "it's $here"})

		if labels["id.label"] != "$$value" {
			t.Errorf("id.label = %q, want %q", labels["id.label"], "$$value")
		}
		// The apostrophe must be preserved; only "$" is doubled.
		if labels["extra"] != "it's $$here" {
			t.Errorf("extra = %q, want %q", labels["extra"], "it's $$here")
		}
	})

	t.Run("splits ID label only on first equals sign", func(t *testing.T) {
		r := &runner{}
		r.IDLabels = []string{"id.label=a=b=c"}

		labels := r.buildServiceLabels(nil)

		if labels["id.label"] != "a=b=c" {
			t.Errorf("id.label = %q, want %q", labels["id.label"], "a=b=c")
		}
	})

	t.Run("additional labels merge alongside ID labels", func(t *testing.T) {
		r := &runner{}
		r.IDLabels = []string{"id.label=v"}

		labels := r.buildServiceLabels(map[string]string{"k": "plain"})

		if labels["id.label"] != "v" {
			t.Errorf("id.label = %q, want %q", labels["id.label"], "v")
		}
		if labels["k"] != "plain" {
			t.Errorf("k = %q, want %q", labels["k"], "plain")
		}
	})
}

func TestResolveServiceEntrypoint(t *testing.T) {
	override := true
	t.Run("override command clears entrypoint and command", func(t *testing.T) {
		entry, cmd := resolveServiceEntrypoint(
			&config.MergedDevContainerConfig{
				DevContainerConfigBase: config.DevContainerConfigBase{OverrideCommand: &override},
			},
			&composetypes.ServiceConfig{Entrypoint: []string{"a"}, Command: []string{"b"}},
			&config.ImageDetails{},
		)
		if len(entry) != 0 || len(cmd) != 0 {
			t.Errorf("expected empty entrypoint/command, got %v / %v", entry, cmd)
		}
	})

	t.Run("falls back to image entrypoint and command", func(t *testing.T) {
		entry, cmd := resolveServiceEntrypoint(
			&config.MergedDevContainerConfig{},
			&composetypes.ServiceConfig{},
			&config.ImageDetails{Config: config.ImageDetailsConfig{
				Entrypoint: []string{"img-entry"},
				Cmd:        []string{"img-cmd"},
			}},
		)
		if len(entry) != 1 || entry[0] != "img-entry" {
			t.Errorf("entrypoint = %v, want [img-entry]", entry)
		}
		if len(cmd) != 1 || cmd[0] != "img-cmd" {
			t.Errorf("command = %v, want [img-cmd]", cmd)
		}
	})
}

func TestNamedVolumesFromMounts(t *testing.T) {
	t.Run("nil when no volume mounts", func(t *testing.T) {
		if got := namedVolumesFromMounts([]*config.Mount{{Type: mountTypeBind}}); got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("collects volume mounts", func(t *testing.T) {
		got := namedVolumesFromMounts([]*config.Mount{
			{Type: mountTypeVolume, Source: "data", External: true},
			{Type: mountTypeBind, Source: "/host"},
		})
		if len(got) != 1 {
			t.Fatalf("expected 1 volume, got %d", len(got))
		}
		v := got["data"]
		if v.Name != "data" || !bool(v.External) {
			t.Errorf("volume = %+v, want name=data external=true", v)
		}
	})

	t.Run("skips anonymous volumes with empty source", func(t *testing.T) {
		got := namedVolumesFromMounts([]*config.Mount{
			{Type: mountTypeVolume, Target: "/anon"},
			{Type: mountTypeVolume, Source: "named"},
		})
		if len(got) != 1 {
			t.Fatalf("expected 1 named volume, got %d: %+v", len(got), got)
		}
		if _, ok := got[""]; ok {
			t.Error("anonymous volume with empty source should not be declared")
		}
	})
}

func TestComposeBuildArgs(t *testing.T) {
	t.Run("adds --pull when requested", func(t *testing.T) {
		args := composeBuildArgs(&composeBuildArgsParams{
			projectName: "ws",
			serviceName: "app",
			pull:        true,
		})
		if !slices.Contains(args, "--pull") {
			t.Errorf("expected --pull in %v", args)
		}
	})

	t.Run("omits --pull by default", func(t *testing.T) {
		args := composeBuildArgs(&composeBuildArgsParams{
			projectName: "ws",
			serviceName: "app",
			pull:        false,
		})
		if slices.Contains(args, "--pull") {
			t.Errorf("did not expect --pull in %v", args)
		}
	})

	t.Run("includes project name, build, and override file", func(t *testing.T) {
		args := composeBuildArgs(&composeBuildArgsParams{
			projectName:             "ws",
			serviceName:             "app",
			overrideComposeFilePath: "/tmp/override.yml",
		})
		if !slices.Contains(args, composeProjectNameFlag) || !slices.Contains(args, "ws") {
			t.Errorf("expected project name flag in %v", args)
		}
		if !slices.Contains(args, "build") {
			t.Errorf("expected build verb in %v", args)
		}
		if !slices.Contains(args, "/tmp/override.yml") {
			t.Errorf("expected override file in %v", args)
		}
	})

	t.Run("appends run services without duplicating the main service", func(t *testing.T) {
		args := composeBuildArgs(&composeBuildArgsParams{
			projectName: "ws",
			serviceName: "app",
			runServices: []string{"app", "db"},
		})
		appCount := 0
		for _, a := range args {
			if a == "app" {
				appCount++
			}
		}
		if appCount != 1 {
			t.Errorf("main service should appear once, got %d in %v", appCount, args)
		}
		if !slices.Contains(args, "db") {
			t.Errorf("expected run service db in %v", args)
		}
	})
}
