package external_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/devsy-org/devsy/cmd"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	containerconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/driver/drivercreate"
	"github.com/devsy-org/devsy/pkg/driver/external"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/suite"
)

func TestMain(m *testing.M) {
	// New launches os.Executable; child invocations must use the same CLI entry
	// point as main.go rather than the fixture's substitute supervisor command.
	if len(os.Args) > 2 && os.Args[1] == "internal" && os.Args[2] == "runtime-supervisor" {
		cmd.Execute()
		return
	}
	os.Exit(m.Run())
}

const noImageBackend = "none"

type ProductionHostSuite struct{ suite.Suite }

func TestProductionHostSuite(t *testing.T) { suite.Run(t, new(ProductionHostSuite)) }

func (s *ProductionHostSuite) TestPreparedRuntimeThroughCLIHelper() {
	s.T().Setenv(config.EnvHome, s.T().TempDir())
	origin := s.T().TempDir()
	directory := filepath.Join(origin, "binaries", "runtime")
	s.Require().NoError(os.MkdirAll(directory, 0o700))
	name := "runtime space é"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(directory, name)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// #nosec G204 -- Builds the checked-in runtime fixture in a private test directory.
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./internal/testfixture")
	output, err := build.CombinedOutput()
	s.Require().NoError(err, string(output))
	// #nosec G304 -- Reads only the executable just built in the test-owned directory.
	data, err := os.ReadFile(binary)
	s.Require().NoError(err)
	checksum := sha256.Sum256(data)
	workspace := &provider.AgentWorkspaceInfo{
		Origin:    origin,
		Workspace: &provider.Workspace{ID: "factory-workspace", UID: "factory-workspace-uid"},
		Agent: provider.ProviderAgentConfig{
			Driver: provider.ExternalDriver,
			Docker: provider.ProviderDockerDriverConfig{Runtime: "docker", Elevation: "none"},
			External: provider.ProviderExternalDriverConfig{
				Binary: "RUNTIME", Args: []string{"--state-dir", s.T().TempDir()},
			},
			Binaries: map[string][]*provider.ProviderBinary{
				"RUNTIME": {{
					OS: runtime.GOOS, Arch: runtime.GOARCH, Path: name,
					Checksum: hex.EncodeToString(checksum[:]),
				}},
			},
		},
	}
	host, err := external.New(ctx, workspace)
	s.Require().NoError(err)
	s.NotEmpty(host.Info().RuntimeName)
	s.Require().NoError(host.Preflight(ctx, driver.PreflightOptions{}))
	s.assertFactoryIntegration(ctx, workspace)

	workspace.Origin = filepath.Join(origin, "missing")
	_, err = external.New(ctx, workspace)
	s.ErrorIs(err, os.ErrNotExist)
}

func (s *ProductionHostSuite) assertFactoryIntegration(
	ctx context.Context,
	workspace *provider.AgentWorkspaceInfo,
) {
	original := workspace.Agent
	defer func() { workspace.Agent = original }()
	for _, backend := range []string{"", provider.DockerDriver, noImageBackend} {
		s.Run("image backend "+backend, func() {
			workspace.Agent.External.ImageBackend = backend
			bundle, err := drivercreate.New(ctx, workspace)
			s.Require().NoError(err)
			s.IsType(&external.Host{}, bundle.Runtime)
			s.Equal(backend != noImageBackend, bundle.Images != nil)
			_, runtimeImages := bundle.Runtime.(driver.ImageBackend)
			s.False(runtimeImages)
			_, reprovision := bundle.Runtime.(driver.ReprovisioningDriver)
			s.False(reprovision)
			if bundle.Images != nil {
				_, dockerBackend := bundle.Images.(driver.DockerHelperProvider)
				s.True(dockerBackend)
			}
		})
	}
	workspace.Agent.External.ImageBackend = noImageBackend
	workspace.Agent.Docker.Builder = "invalid-builder"
	bundle, err := drivercreate.New(ctx, workspace)
	s.Require().NoError(err)
	s.Nil(bundle.Images)
	s.assertRunnerLifecycle(ctx, workspace, bundle.Runtime.(driver.ImageRunner))

	workspace.Agent.External.ImageBackend = provider.DockerDriver
	_, err = drivercreate.New(ctx, workspace)
	s.ErrorContains(err, "external runtime image backend")
	workspace.Agent.External.ImageBackend = "unsupported"
	_, err = drivercreate.New(ctx, workspace)
	s.ErrorContains(err, "imageBackend must be docker or none")
}

func (s *ProductionHostSuite) assertRunnerLifecycle(
	ctx context.Context, workspace *provider.AgentWorkspaceInfo, imageRunner driver.ImageRunner,
) {
	runner, err := devcontainer.NewRunner(ctx, "", "", workspace)
	s.Require().NoError(err)
	found, err := runner.Find(ctx)
	s.Require().NoError(err)
	s.Nil(found)
	s.Require().NoError(imageRunner.RunImageDevContainer(ctx, &driver.RunImageDevContainerParams{
		WorkspaceID: devcontainer.GetRunnerIDFromWorkspace(workspace.Workspace),
		Options: &driver.RunOptions{
			Image: "fixture",
			User:  "root",
			Env:   map[string]string{"TOKEN": "factory-secret"},
			WorkspaceMount: &containerconfig.Mount{
				Type:   driver.MountTypeBind,
				Source: "/host/workspace",
				Target: "/workspace",
			},
		},
	}))
	found, err = runner.Find(ctx)
	s.Require().NoError(err)
	s.Require().NotNil(found)
	s.Equal(containerconfig.ContainerStatusRunning, found.State.Status)
	s.Equal("/workspace", found.Mounts[0].Destination)
	var stdout, stderr bytes.Buffer
	s.Require().NoError(runner.Command(ctx, devcontainer.CommandParams{
		Command: "cat",
		Stdin:   bytes.NewBufferString("literal-input"),
		Stdout:  &stdout,
		Stderr:  &stderr,
	}))
	s.Equal("literal-input", stdout.String())
	var logs bytes.Buffer
	s.Require().NoError(runner.Logs(ctx, &logs))
	s.NotEmpty(logs.String())
	s.Require().NoError(runner.Stop(ctx))
	found, err = runner.Find(ctx)
	s.Require().NoError(err)
	s.Equal(containerconfig.ContainerStatusExited, found.State.Status)
	s.Require().NoError(runner.Delete(ctx, devcontainer.DeleteOptions{}))
	found, err = runner.Find(ctx)
	s.Require().NoError(err)
	s.Nil(found)
}
