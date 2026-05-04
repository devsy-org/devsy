package feature

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type OCIFeatureTestSuite struct {
	suite.Suite
}

func TestOCIFeatureTestSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping OCI pull test in short mode")
	}
	suite.Run(t, new(OCIFeatureTestSuite))
}

func (s *OCIFeatureTestSuite) TestProcessOCIFeature_HappyPath() {
	dir := s.T().TempDir()
	orig := os.Getenv("XDG_CACHE_HOME")
	s.T().Setenv("XDG_CACHE_HOME", dir)
	defer func() {
		if orig != "" {
			_ = os.Setenv("XDG_CACHE_HOME", orig)
		}
	}()

	result, err := processOCIFeature("ghcr.io/devcontainers/features/go:1")
	s.Require().NoError(err)
	s.DirExists(result)
	s.FileExists(filepath.Join(result, "devcontainer-feature.json"))
}

func (s *OCIFeatureTestSuite) TestFetchCollection_GHCR() {
	collection, err := FetchCollection("ghcr.io", "devcontainers/features")
	if err != nil {
		s.T().Skipf("skipping: collection not available from ghcr.io: %v", err)
	}
	s.Require().NotNil(collection)
	s.NotEmpty(collection.Features)

	var foundGo bool
	for _, f := range collection.Features {
		s.NotEmpty(f.ID)
		s.NotEmpty(f.Version)
		if f.ID == "go" {
			foundGo = true
		}
	}
	s.True(foundGo, "expected 'go' feature in ghcr.io/devcontainers/features collection")
}
