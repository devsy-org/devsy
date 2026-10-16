package compose

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/wslg"
	"github.com/stretchr/testify/suite"
)

type WSLgSuite struct{ suite.Suite }

func TestWSLgSuite(t *testing.T) { suite.Run(t, new(WSLgSuite)) }

func (s *WSLgSuite) TestLoaderRequiredInterpolationAndEmptyValues() {
	path := s.fixture(
		`services:
  dev:
    image: alpine
    environment:
      DISPLAY: ${DEVSY_WSLG_DISPLAY:?required}
      EMPTY: ${DEVSY_WSLG_XDG_RUNTIME_DIR-default}
      ABSENT: ${DEVSY_WSLG_WAYLAND_DISPLAY-default}
`,
	)
	_, err := LoadDockerComposeProject(context.Background(), []string{path}, nil)
	s.Error(err)
	project, err := LoadDockerComposeProjectWithEnv(
		context.Background(),
		[]string{path},
		nil,
		map[string]string{wslg.Display: ":0$literal", wslg.RuntimeDir: ""},
	)
	s.Require().NoError(err)
	s.Equal(":0$literal", *project.Services["dev"].Environment["DISPLAY"])
	s.Equal("", *project.Services["dev"].Environment["EMPTY"])
	s.Equal("default", *project.Services["dev"].Environment["ABSENT"])
	_, set := os.LookupEnv(wslg.Display)
	s.False(set)
}

func (s *WSLgSuite) TestDotenvCollisionsIncludingEmptyAndShadowed() {
	path := s.fixture("services:\n  dev:\n    image: alpine\n")
	file := filepath.Join(filepath.Dir(path), ".env")
	for _, line := range []string{
		"DEVSY_WSLG_DISPLAY=",
		"export DEVSY_WSLG_WAYLAND_DISPLAY='hidden'",
		"DEVSY_WSLG_XDG_RUNTIME_DIR=first\nDEVSY_WSLG_XDG_RUNTIME_DIR=",
	} {
		s.Require().NoError(os.WriteFile(file, []byte(line), 0o600))
		for _, files := range [][]string{nil, {file}} {
			_, err := LoadDockerComposeProjectWithEnv(
				context.Background(),
				[]string{path},
				files,
				map[string]string{wslg.Display: ":0"},
			)
			s.ErrorIs(err, wslg.ErrCollision)
			if err != nil {
				s.NotContains(err.Error(), "hidden")
			}
		}
		_, err := LoadDockerComposeProject(context.Background(), []string{path}, []string{file})
		s.NoError(err, "disabled path preserves normal dotenv precedence")
	}
}

func (s *WSLgSuite) TestServiceEnvFileIsNotInterpolationSource() {
	path := s.fixture(
		`services:
  dev:
    image: alpine
    env_file: ./service.env
    environment:
      DISPLAY: ${DEVSY_WSLG_DISPLAY:?required}
`,
	)
	s.Require().NoError(os.WriteFile(
		filepath.Join(filepath.Dir(path), "service.env"),
		[]byte("DEVSY_WSLG_DISPLAY=service-only"), 0o600,
	))
	project, err := LoadDockerComposeProjectWithEnv(
		context.Background(),
		[]string{path},
		nil,
		map[string]string{wslg.Display: ":0"},
	)
	s.Require().NoError(err)
	s.Equal("service-only", *project.Services["dev"].Environment[wslg.Display])
	s.Equal(":0", *project.Services["dev"].Environment["DISPLAY"])
}

func (s *WSLgSuite) TestScopedChildEnvironment() {
	helper := &ComposeHelper{
		Command: "unused",
		Docker: &docker.DockerHelper{
			Environment: []string{"PROVIDER_VAR=keep", wslg.Display + "=old"},
		},
	}
	env := map[string]string{wslg.Display: ":0", wslg.RuntimeDir: ""}
	scoped := helper.WithInvocationEnvironment(env)
	env[wslg.Display] = "mutated"
	cmd := scoped.buildCmd(context.Background(), "config")
	s.Contains(cmd.Env, "PROVIDER_VAR=keep")
	s.Contains(cmd.Env, wslg.Display+"=:0")
	s.Contains(cmd.Env, wslg.RuntimeDir+"=")
	s.NotContains(cmd.Env, wslg.Display+"=old")
	s.Nil(helper.invocationEnvironment)
}

// This exercises both consumers without a daemon. CI installs Compose on the
// Windows WSL job and requires this test rather than accepting a local skip.
func (s *WSLgSuite) TestInstalledComposeParity() {
	path, err := exec.LookPath("docker")
	if err != nil {
		s.T().Skip("Docker CLI unavailable")
	}
	//nolint:gosec // Resolve the installed Docker CLI for this integration test.
	if err := exec.Command(path, "compose", "version").Run(); err != nil {
		s.T().Skip("Compose unavailable")
	}
	file := s.fixture(
		`name: wslg-parity
services:
  dev:
    image: alpine
    environment:
      DISPLAY: ${DEVSY_WSLG_DISPLAY:?required}
      EMPTY: ${DEVSY_WSLG_XDG_RUNTIME_DIR-default}
`,
	)
	candidates := map[string]string{wslg.Display: ":0$literal", wslg.RuntimeDir: ""}
	project, err := LoadDockerComposeProjectWithEnv(
		context.Background(),
		[]string{file},
		nil,
		candidates,
	)
	s.Require().NoError(err)
	helper := (&ComposeHelper{Command: path, Args: []string{"compose"}}).WithInvocationEnvironment(
		candidates,
	)
	var stdout, stderr bytes.Buffer
	err = helper.Run(
		context.Background(),
		[]string{"-f", file, "config", "--format", "json"},
		nil,
		&stdout,
		&stderr,
	)
	s.Require().NoError(err, stderr.String())
	// `compose config` escapes dollars for a reusable Compose document. Load
	// that document once to compare runtime values, not its serialized escaping.
	rendered := filepath.Join(s.T().TempDir(), "rendered.json")
	s.Require().NoError(os.WriteFile(rendered, stdout.Bytes(), 0o600))
	actual, err := LoadDockerComposeProject(context.Background(), []string{rendered}, nil)
	s.Require().NoError(err)
	for _, key := range []string{"DISPLAY", "EMPTY"} {
		s.Equal(*project.Services["dev"].Environment[key], *actual.Services["dev"].Environment[key])
	}
	s.T().Logf("loader/CLI parity on %s", runtime.GOOS)
}

func (s *WSLgSuite) fixture(content string) string {
	path := filepath.Join(s.T().TempDir(), "compose.yaml")
	s.Require().NoError(os.WriteFile(path, []byte(content), 0o600))
	return path
}
