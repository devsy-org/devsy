package devcontainer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	"github.com/devsy-org/devsy/pkg/compose"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/wslg"
	"github.com/stretchr/testify/suite"
	"gopkg.in/yaml.v3"
)

const wslgTestContext = "wslg-test"

type WSLgSuite struct{ suite.Suite }

func TestWSLgSuite(t *testing.T) { suite.Run(t, new(WSLgSuite)) }

func (s *WSLgSuite) TestSubstitutionIsPerWorkspaceAndDoesNotInjectContainerEnv() {
	first, second := newTestRunner(&mockDriver{}), newTestRunner(&mockDriver{})
	first.workspaceConfig.Workspace = &provider.Workspace{ID: "first"}
	second.workspaceConfig.Workspace = &provider.Workspace{ID: "second"}
	first.wslgSnapshot, second.wslgSnapshot = s.snapshot(":0"), s.snapshot(":1")
	raw := &config.DevContainerConfig{}
	raw.Image = "${localEnv:DEVSY_WSLG_DISPLAY}"
	for _, r := range []*runner{first, second} {
		parsed, ctx, err := r.substitute(
			provider.CLIOptions{InitEnv: []string{"CUSTOM=override"}},
			raw,
		)
		s.Require().NoError(err)
		s.Equal(r.wslgSnapshot.Environment()[wslg.Display], parsed.Config.Image)
		s.Equal("override", ctx.Env["CUSTOM"])
		s.Empty(parsed.Config.ContainerEnv)
		_, ok := ctx.Env[wslg.WaylandDisplay]
		s.False(ok)
	}
	_, ok := os.LookupEnv(wslg.Display)
	s.False(ok)
}

func (s *WSLgSuite) TestCollisionSources() {
	r := newTestRunner(&mockDriver{})
	s.ErrorIs(
		r.checkWSLgCollisions(provider.CLIOptions{InitEnv: []string{wslg.Display + "="}}),
		wslg.ErrCollision,
	)
	r.workspaceConfig.Agent.Docker.Env = map[string]string{wslg.WaylandDisplay: "private"}
	err := r.checkWSLgCollisions(provider.CLIOptions{})
	s.ErrorIs(err, wslg.ErrCollision)
	s.NotContains(err.Error(), "private")
	r.workspaceConfig.Agent.Docker.Env = nil
	s.T().Setenv(wslg.RuntimeDir, "private")
	err = r.checkWSLgCollisions(provider.CLIOptions{})
	s.ErrorIs(err, wslg.ErrCollision)
	s.NotContains(err.Error(), "private")
}

func (s *WSLgSuite) TestReuseRequiresRecreateForStaleStoppedAndRevoked() {
	r := newTestRunner(&mockDriver{})
	r.wslgSnapshot = s.snapshot(":0")
	labels, err := r.wslgLabels()
	s.Require().NoError(err)
	details := &config.ContainerDetails{}
	details.Config.Labels = labels
	details.State.Status = config.ContainerStatusRunning
	s.NoError(r.checkWSLgReuse(details, false))
	details.State.Status = config.ContainerStatusExited
	s.ErrorContains(r.checkWSLgReuse(details, false), "--recreate")
	s.NoError(r.checkWSLgReuse(details, true))
	details.State.Status = config.ContainerStatusRunning
	r.wslgSnapshot = s.snapshot(":1")
	s.ErrorContains(r.checkWSLgReuse(details, false), "--recreate")
	r.wslgSnapshot = nil
	s.ErrorContains(r.checkWSLgReuse(details, false), "--recreate")
	details.Config.Labels = nil
	s.NoError(r.checkWSLgReuse(details, false))
	r.wslgSnapshot = s.snapshot(":0")
	s.ErrorContains(r.checkWSLgReuse(details, false), "--recreate")
}

func (s *WSLgSuite) TestV2InvocationLabelRoundTripAndTeardown() {
	dir := s.T().TempDir()
	base := filepath.Join(dir, "compose.yaml")
	override := filepath.Join(dir, "override.yaml")
	s.Require().
		NoError(os.WriteFile(base, []byte(`services:
  dev:
    image: alpine
    environment:
      DISPLAY: ${DEVSY_WSLG_DISPLAY:?required}
`), 0o600))
	s.Require().
		NoError(os.WriteFile(override, []byte("services:\n  dev:\n    labels: {}\n"), 0o600))
	r := newTestRunner(&mockDriver{})
	r.wslgSnapshot = s.snapshot(":0$literal$$dollars")
	invocation := composeInvocation{
		Version:   2,
		Directory: dir,
		Args:      []string{"-f", base, "-f", override},
		WSLg:      r.wslgReplay(),
	}
	s.Require().
		NoError(recordComposeInvocation(composeInvocationRecord{Path: override, Service: "dev", Invocation: invocation}))
	project, err := compose.LoadDockerComposeProjectWithEnv(
		context.Background(),
		[]string{base, override},
		nil,
		r.wslgSnapshot.Environment(),
	)
	s.Require().NoError(err)
	value := project.Services["dev"].Labels[composeInvocationLabel]
	var actual composeInvocation
	s.Require().NoError(json.Unmarshal([]byte(value), &actual))
	s.Equal(invocation, actual)
	details := &config.ContainerDetails{}
	details.Config.Labels = map[string]string{
		pkgconfig.ComposeProjectLabel: wslgTestContext,
		composeInvocationLabel:        value,
	}
	r.driver = &mockDriver{findResult: details}
	// Deliberately clear all discovery state; cleanup uses only recorded input.
	r.wslgSnapshot = nil
	replay, err := r.composeTeardownArgs(context.Background(), wslgTestContext)
	s.Require().NoError(err)
	s.Equal(":0$literal$$dollars", replay.WSLgEnv[wslg.Display])
	s.Equal(append([]string{"--project-directory", dir}, invocation.Args...), replay.Args)
}

func (s *WSLgSuite) TestCorruptReplayFailsClosed() {
	good := composeInvocation{
		Version:   2,
		Directory: "project",
		Args:      []string{"-f", "compose.yaml"},
		WSLg: &wslgReplay{
			Target:      wslg.Target{Distro: "Ubuntu", User: "alice"},
			Environment: map[string]string{wslg.Display: ":0"},
		},
	}
	s.NoError(validateComposeInvocation(good))
	bad := good
	bad.WSLg = nil
	s.Error(validateComposeInvocation(bad))
	bad = good
	bad.Version = 3
	s.Error(validateComposeInvocation(bad))
	bad = good
	bad.Args = []string{"--privileged", "yes"}
	s.Error(validateComposeInvocation(bad))
	bad = good
	bad.WSLg = &wslgReplay{
		Target:      good.WSLg.Target,
		Environment: map[string]string{"SSH_AUTH_SOCK": "private"},
	}
	s.Error(validateComposeInvocation(bad))
	bad = good
	bad.WSLg = &wslgReplay{
		Target:      good.WSLg.Target,
		Environment: map[string]string{wslg.Display: "bad\nvalue"},
	}
	s.Error(validateComposeInvocation(bad))
}

func (s *WSLgSuite) TestContainerEnvSurvivesComposeInterpolation() {
	r := newTestRunner(&mockDriver{})
	r.wslgSnapshot = s.snapshot(":0$literal$$dollars")
	env := map[string]string{"DISPLAY": r.wslgSnapshot.Environment()[wslg.Display]}
	project := &composetypes.Project{
		Services: composetypes.Services{
			"dev": composetypes.ServiceConfig{
				Image:       exampleBaseImage,
				Environment: r.composeContainerEnvironment(env),
			},
		},
	}
	data, err := yaml.Marshal(project)
	s.Require().NoError(err)
	path := filepath.Join(s.T().TempDir(), "compose.yaml")
	s.Require().NoError(os.WriteFile(path, data, 0o600))
	loaded, err := compose.LoadDockerComposeProjectWithEnv(
		context.Background(),
		[]string{path},
		nil,
		r.wslgSnapshot.Environment(),
	)
	s.Require().NoError(err)
	s.Equal(env["DISPLAY"], *loaded.Services["dev"].Environment["DISPLAY"])
	s.Equal(":0$literal$$dollars", env["DISPLAY"], "do not mutate merged configuration")
}

func (s *WSLgSuite) TestLocalAuthorizationOffAndUnsupported() {
	s.T().Setenv(pkgconfig.EnvHome, s.T().TempDir())
	s.T().Setenv(pkgconfig.EnvConfig, "")
	s.T().Setenv(wslg.Display, "ordinary-user-value")
	r := newTestRunner(&mockDriver{})
	r.workspaceConfig.Workspace = &provider.Workspace{ID: wslgTestContext, Context: wslgTestContext}
	cfg, err := pkgconfig.LoadConfig(wslgTestContext, "")
	s.Require().NoError(err)
	s.Require().NoError(r.prepareWSLg(context.Background(), provider.CLIOptions{}))
	s.Nil(r.wslgSnapshot)
	raw := &config.DevContainerConfig{}
	raw.Image = "${localEnv:DEVSY_WSLG_DISPLAY}"
	parsed, _, err := r.substitute(provider.CLIOptions{}, raw)
	s.Require().NoError(err)
	s.Equal("ordinary-user-value", parsed.Config.Image)
	cfg.Current().Options[pkgconfig.ContextOptionWSLgEnabled] = pkgconfig.OptionValue{
		Value: pkgconfig.BoolTrue,
	}
	s.Require().NoError(pkgconfig.SaveConfig(cfg))
	// The resolved mock driver is unsupported on every OS. The owner opt-in
	// must fail before attempting any WSL command or colliding with process env.
	s.ErrorIs(r.prepareWSLg(context.Background(), provider.CLIOptions{}), wslg.ErrUnsupported)
}

func (s *WSLgSuite) TestReplayRejectsTrailingAndUnknownData() {
	for _, value := range []string{`{"version":1,"extra":"ignored"}`, `{"version":2} trailing`, string([]byte{0xff})} {
		s.Error(decodeWSLgMetadata(value, &composeInvocation{}))
	}
}

func (s *WSLgSuite) snapshot(display string) *wslg.Snapshot {
	snap, err := wslg.NewSnapshot(
		wslg.Target{Distro: "Ubuntu", User: "alice"},
		map[string]string{wslg.Display: display, wslg.RuntimeDir: ""},
	)
	s.Require().NoError(err)
	return snap
}
