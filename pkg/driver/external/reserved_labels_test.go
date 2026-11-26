package external

import (
	"context"
	"encoding/json"
	"time"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
)

func (s *HostSuite) TestReservedIdentityLabelsNeverReachRuntimeProcess() {
	host := s.host("normal")
	params := &driver.RunImageDevContainerParams{
		WorkspaceID: fixtureWorkspace,
		Options:     &driver.RunOptions{Image: fixtureImage},
	}
	for _, key := range []string{
		"devsy.user", "sh.devsy.recovery", "devsy.overlay.structure", "devsy.sh/metadata-creation-config",
		"devsy.compose.contract", "devsy.compose.invocation", "devsy.wslg.snapshot",
		"devsy.sh/user", "devsy.sh/microsandbox-workspace-mount", "devsy.sh/microsandbox-workspace-user",
		"sh.devsy.managed", "sh.devsy.workspace-id", "sh.devsy.resource", "sh.devsy.volume-role", "sh.devsy.seeded",
		"devsy.sh/created", "devsy.sh/workspace-uid", "devsy.sh/managed", "devsy.sh/resource", "devsy.sh/volume-role",
	} {
		s.Run(key, func() {
			host.idLabels = []string{key + "=fixture-private-value"}
			s.ErrorContains(host.ValidateRunImageDevContainer(params), key)
			s.ErrorContains(host.RunImageDevContainer(s.T().Context(), params), key)
			found, err := host.FindDevContainer(s.T().Context(), fixtureWorkspace)
			s.Require().NoError(err)
			s.Nil(found)
		})
	}
}

func (s *HostSuite) TestGeneratedAndOrdinaryLabelsThroughRuntimeProcess() {
	host := s.host("normal")
	host.idLabels = []string{"custom.label=ordinary=value", "dev.containers.id=custom-id"}
	generated := []string{
		pkgconfig.DockerUserLabel + "=root",
		pkgconfig.DockerRecoveryLabel + "=true",
		pkgconfig.OverlayStructureLabel + "=v1:fingerprint",
		pkgconfig.CreationConfigLabel + "=true",
		pkgconfig.DevcontainerMetadataLabel + `=[{"remoteEnv":{"TOKEN":"private"}}]`,
	}
	params := &driver.RunImageDevContainerParams{
		WorkspaceID: fixtureWorkspace,
		Options:     &driver.RunOptions{Image: fixtureImage, Labels: generated},
	}
	ctx, cancel := context.WithTimeout(s.T().Context(), 30*time.Second)
	defer cancel()
	s.Require().NoError(host.ValidateRunImageDevContainer(params))
	s.Require().NoError(host.RunImageDevContainer(ctx, params))
	found, err := host.FindDevContainer(ctx, fixtureWorkspace)
	s.Require().NoError(err)
	s.Require().NotNil(found)
	want := config.ListToObject(append(append([]string(nil), generated...), host.idLabels...))
	want["fake.remote-user"] = fixtureProcessUser
	s.Equal(want, found.Config.Labels)
	s.Equal(generated, params.Options.Labels)
}

func (s *HostSuite) TestDevcontainerLabelInputsCannotBecomeRequestLabels() {
	host := s.host("normal")
	for _, raw := range []string{
		`{"image":"fixture","containerLabels":{"devsy.user":"private"}}`,
		`{"image":"fixture","labels":{"devsy.user":"private"}}`,
	} {
		parsed := &config.DevContainerConfig{}
		s.Require().NoError(json.Unmarshal([]byte(raw), parsed))
		params := &driver.RunImageDevContainerParams{
			WorkspaceID:  fixtureWorkspace,
			ParsedConfig: parsed,
			Options: &driver.RunOptions{
				Image:  parsed.Image,
				Labels: []string{"devsy.user=root"},
			},
		}
		s.Require().NoError(host.RunImageDevContainer(s.T().Context(), params))
		found, err := host.FindDevContainer(s.T().Context(), fixtureWorkspace)
		s.Require().NoError(err)
		s.Require().NotNil(found)
		s.Equal(fixtureProcessUser, found.Config.Labels["devsy.user"])
		s.Equal(
			map[string]string{
				"devsy.user":        fixtureProcessUser,
				"dev.containers.id": fixtureWorkspace,
				"fake.remote-user":  fixtureProcessUser,
			},
			found.Config.Labels,
		)
		s.Require().NoError(host.DeleteDevContainer(s.T().Context(), fixtureWorkspace))
	}
	parsed := &config.DevContainerConfig{}
	s.Require().
		NoError(json.Unmarshal([]byte(`{"image":"fixture","runArgs":["--label","devsy.user=private"]}`), parsed))
	params := &driver.RunImageDevContainerParams{
		WorkspaceID:  fixtureWorkspace,
		ParsedConfig: parsed,
		Options:      &driver.RunOptions{Image: parsed.Image},
	}
	s.ErrorContains(host.RunImageDevContainer(s.T().Context(), params), "cannot express runArgs")
	found, err := host.FindDevContainer(s.T().Context(), fixtureWorkspace)
	s.Require().NoError(err)
	s.Nil(found)
}
