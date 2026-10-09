package external

import (
	"context"

	"github.com/devsy-org/devsy-runtime-sdk/conformance/fake"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *HostSuite) TestReusePreflightAcrossFreshProcesses() {
	host := s.host(fake.Normal)
	ctx := context.Background()
	s.Require().NoError(host.RunImage(ctx, &runtimev1.RunImageRequest{
		WorkspaceId: fixtureWorkspace,
		Image:       fixtureImage,
		User:        fixtureProcessUser,
		RemoteUser:  fixtureRemoteUser,
	}))
	s.Require().NoError(host.StopDevContainer(ctx, fixtureWorkspace))
	before, err := host.FindDevContainer(ctx, fixtureWorkspace)
	s.Require().NoError(err)
	s.NoError(host.ReusePreflight(ctx, fixtureWorkspace, fixtureRemoteUser))
	err = host.ReusePreflight(ctx, fixtureWorkspace, fixtureProcessUser)
	s.Equal(codes.FailedPrecondition, status.Code(err))
	s.ErrorContains(err, "--recreate")
	after, err := host.FindDevContainer(ctx, fixtureWorkspace)
	s.Require().NoError(err)
	s.Equal(before, after)
	s.Equal(codes.NotFound, status.Code(host.ReusePreflight(ctx, "absent", fixtureProcessUser)))
	s.ErrorContains(host.ReusePreflight(ctx, "", fixtureProcessUser), "workspace ID")
	s.ErrorContains(host.ReusePreflight(ctx, fixtureWorkspace, ""), "remote user")
}

func (s *HostSuite) TestReusePreflightCapabilityNegotiation() {
	host := s.host(fake.Normal)
	s.True(host.SupportsReusePreflight())
	host.info.Capabilities.ReusePreflight = false
	s.False(host.SupportsReusePreflight())
	// An invalid executable proves the optional RPC never launches a runtime.
	host.config.Binaries[fixtureKey][0].Checksum = "invalid"
	s.NoError(host.ReusePreflight(context.Background(), fixtureWorkspace, fixtureProcessUser))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.ErrorIs(host.ReusePreflight(ctx, fixtureWorkspace, fixtureProcessUser), context.Canceled)
}
