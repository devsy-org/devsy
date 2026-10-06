package external_test

import (
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
	"github.com/devsy-org/devsy/pkg/driver"
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
		Origin: origin,
		Agent: provider.ProviderAgentConfig{
			Driver: provider.ExternalDriver,
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

	workspace.Origin = filepath.Join(origin, "missing")
	_, err = external.New(ctx, workspace)
	s.ErrorIs(err, os.ErrNotExist)
}
