package external

import (
	"context"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy/pkg/driver"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *HostSuite) TestNegotiatedRecreationAcrossFreshProcesses() {
	host := s.host("recreate-on-run")
	s.Equal(driver.RecreateOnRun, host.RecreateMode())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	params := &driver.RunImageDevContainerParams{
		WorkspaceID: fixtureWorkspace,
		Options: &driver.RunOptions{
			Image: fixtureImage, Labels: []string{"generation=original"},
		},
	}
	s.Require().NoError(host.RunImageDevContainer(ctx, params))
	original, err := host.FindDevContainer(ctx, fixtureWorkspace)
	s.Require().NoError(err)
	s.Require().NotNil(original)
	err = host.RunImage(
		ctx,
		&runtimev1.RunImageRequest{WorkspaceId: fixtureWorkspace, Image: fixtureImage},
	)
	s.Equal(codes.AlreadyExists, status.Code(err))
	params.Options.AllowRecreate = true
	params.Options.Labels = []string{"generation=replaced"}
	s.Require().NoError(host.RunImageDevContainer(ctx, params))
	replaced, err := host.FindDevContainer(ctx, fixtureWorkspace)
	s.Require().NoError(err)
	s.Require().NotNil(replaced)
	s.Equal(original.ID, replaced.ID)
	s.Equal("replaced", replaced.Config.Labels["generation"])
	params.Options.AllowRecreate = false
	s.Equal(codes.AlreadyExists, status.Code(host.RunImageDevContainer(ctx, params)))
	again, err := host.FindDevContainer(ctx, fixtureWorkspace)
	s.Require().NoError(err)
	s.Equal(replaced, again)
}
