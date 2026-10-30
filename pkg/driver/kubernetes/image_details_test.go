package kubernetes

import (
	"os"
	"path/filepath"
	"testing"

	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/suite"
)

const testImage = "registry.example.com/team/image@sha256:abc"

type ImageDetailsSuite struct {
	suite.Suite
}

func TestImageDetailsSuite(t *testing.T) {
	suite.Run(t, new(ImageDetailsSuite))
}

func (s *ImageDetailsSuite) TestNotConfigured() {
	details, err := s.driver("").ImageDetails(testImage)
	s.Require().NoError(err)
	s.Nil(details)
}

func (s *ImageDetailsSuite) TestImageDetailsJSON() {
	path := s.writeFile(`{
		"ID": "custom-id",
		"Config": {
			"User": "vscode",
			"Env": ["PATH=/usr/bin"],
			"Labels": {"devcontainer.metadata": "[]"},
			"Entrypoint": ["/entrypoint.sh"],
			"Cmd": ["sleep", "infinity"]
		}
	}`)

	details, err := s.driver(path).ImageDetails(testImage)
	s.Require().NoError(err)
	s.Equal("custom-id", details.ID)
	s.Equal("vscode", details.Config.User)
	s.Equal([]string{"PATH=/usr/bin"}, details.Config.Env)
	s.Equal(map[string]string{"devcontainer.metadata": "[]"}, details.Config.Labels)
	s.Equal([]string{"/entrypoint.sh"}, details.Config.Entrypoint)
	s.Equal([]string{"sleep", "infinity"}, details.Config.Cmd)
}

func (s *ImageDetailsSuite) TestOCIImageConfig() {
	// Output shape of `crane config <image>`.
	path := s.writeFile(`{
		"architecture": "amd64",
		"os": "linux",
		"config": {"User": "vscode", "Env": ["PATH=/usr/bin"]},
		"rootfs": {"type": "layers", "diff_ids": []}
	}`)

	details, err := s.driver(path).ImageDetails(testImage)
	s.Require().NoError(err)
	s.Equal(testImage, details.ID)
	s.Equal("amd64", details.Architecture)
	s.Equal("linux", details.OS)
	s.Equal("vscode", details.Config.User)
}

func (s *ImageDetailsSuite) TestMissingFile() {
	path := filepath.Join(s.T().TempDir(), "missing.json")
	_, err := s.driver(path).ImageDetails(testImage)
	s.Require().ErrorContains(err, "read image config file")
}

func (s *ImageDetailsSuite) TestInvalidContent() {
	tests := map[string]string{
		"empty":     "",
		"array":     `[{"Config": {"User": "vscode"}}]`,
		"bad json":  `{"Config": `,
		"no config": `{"schemaVersion": 2, "layers": []}`,
	}
	for name, content := range tests {
		s.Run(name, func() {
			_, err := s.driver(s.writeFile(content)).ImageDetails(testImage)
			s.Error(err)
		})
	}
}

func (s *ImageDetailsSuite) writeFile(content string) string {
	path := filepath.Join(s.T().TempDir(), "image-config.json")
	s.Require().NoError(os.WriteFile(path, []byte(content), 0o600))
	return path
}

func (s *ImageDetailsSuite) driver(path string) *KubernetesDriver {
	return &KubernetesDriver{
		options: &provider2.ProviderKubernetesDriverConfig{ImageConfigFile: path},
	}
}
