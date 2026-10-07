package external

import (
	"context"
	"strconv"
	"time"

	"github.com/devsy-org/devsy/pkg/driver"
)

const (
	fixtureProcessUser = "root"
	fixtureRemoteUser  = "developer"
)

func (s *HostSuite) TestWorkspaceIdentityThroughRuntimeProcess() {
	for _, tc := range []struct {
		name       string
		user       string
		remoteUser string
		dockerless bool
	}{
		{"dockerless developer", fixtureProcessUser, fixtureRemoteUser, true},
		{"prebuilt developer", fixtureProcessUser, fixtureRemoteUser, false},
		{"numeric developer", fixtureProcessUser, "1000:1001", true},
		{"empty developer identity", fixtureProcessUser, "", false},
		{"unset identities", "", "", false},
	} {
		s.Run(tc.name, func() {
			host := s.host("workspace-identity")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			s.Require().NoError(host.RunImageDevContainer(ctx, &driver.RunImageDevContainerParams{
				WorkspaceID: fixtureWorkspace,
				Options: &driver.RunOptions{
					Image: fixtureImage, User: tc.user,
					RemoteUser: tc.remoteUser, Dockerless: tc.dockerless,
				},
			}))
			found, err := host.FindDevContainer(ctx, fixtureWorkspace)
			s.Require().NoError(err)
			s.Require().NotNil(found)
			s.Equal(tc.user, found.Config.User)
			s.Equal(tc.remoteUser, found.Config.Labels["fixture.remote-user"])
			s.Equal(strconv.FormatBool(tc.dockerless), found.Config.Labels["fixture.dockerless"])
		})
	}
}
