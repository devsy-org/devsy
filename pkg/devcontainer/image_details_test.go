package devcontainer

import (
	"context"
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/suite"
)

const testImageUser = "vscode"

// imageDetailsDriver supplies image details and fails TargetArchitecture, so
// any fallback to the registry inspect makes the test fail.
type imageDetailsDriver struct {
	failingArchitectureDriver
	details *config.ImageDetails
	err     error
}

func (d *imageDetailsDriver) ImageDetails(string) (*config.ImageDetails, error) {
	return d.details, d.err
}

type ImageDetailsSuite struct {
	suite.Suite
}

func TestImageDetailsSuite(t *testing.T) {
	suite.Run(t, new(ImageDetailsSuite))
}

func (s *ImageDetailsSuite) TestUsesConfiguredDetailsWithoutInspect() {
	d := &imageDetailsDriver{
		failingArchitectureDriver: failingArchitectureDriver{err: errors.New("inspect called")},
		details: &config.ImageDetails{
			ID: "registry.example.com/team/image@sha256:abc",
			Config: config.ImageDetailsConfig{
				User: testImageUser,
				Labels: map[string]string{
					"devcontainer.metadata": `[{"remoteUser":"dev","containerEnv":{"FOO":"bar"}}]`,
				},
			},
		},
	}
	r := newTestRunner(d)

	info, err := r.getImageBuildInfoFromImage(
		context.Background(),
		&config.SubstitutionContext{},
		"registry.example.com/team/image@sha256:abc",
	)
	s.Require().NoError(err)
	s.Equal(testImageUser, info.User)
	s.Same(d.details, info.ImageDetails)
	s.Require().Len(info.Metadata.Config, 1)
	s.Equal("dev", info.Metadata.Config[0].RemoteUser)
	s.Equal("bar", info.Metadata.Config[0].ContainerEnv["FOO"])
}

func (s *ImageDetailsSuite) TestReturnsConfiguredDetailsError() {
	d := &imageDetailsDriver{
		failingArchitectureDriver: failingArchitectureDriver{err: errors.New("inspect called")},
		err:                       errors.New("bad image details"),
	}
	r := newTestRunner(d)

	_, err := r.getImageBuildInfoFromImage(
		context.Background(),
		&config.SubstitutionContext{},
		"image",
	)
	s.Require().EqualError(err, "bad image details")
}

func (s *ImageDetailsSuite) TestFallsBackToInspectWhenNotConfigured() {
	d := &imageDetailsDriver{
		failingArchitectureDriver: failingArchitectureDriver{err: errors.New("inspect called")},
	}
	r := newTestRunner(d)

	_, err := r.getImageBuildInfoFromImage(
		context.Background(),
		&config.SubstitutionContext{},
		"image",
	)
	s.Require().ErrorContains(err, "inspect called")
}
