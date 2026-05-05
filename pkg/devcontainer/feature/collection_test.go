package feature

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/stretchr/testify/suite"
)

const (
	testFeatureNode  = "node"
	testVersion1_0_0 = "1.0.0"
)

type CollectionTestSuite struct {
	suite.Suite
	server  *httptest.Server
	regHost string
}

func TestCollectionTestSuite(t *testing.T) {
	suite.Run(t, new(CollectionTestSuite))
}

func (s *CollectionTestSuite) SetupSuite() {
	s.server = httptest.NewServer(registry.New())
	s.regHost = strings.TrimPrefix(s.server.URL, "http://")
}

func (s *CollectionTestSuite) TearDownSuite() {
	s.server.Close()
}

func (s *CollectionTestSuite) TestFetchCollection_HappyPath() {
	collection := Collection{
		Features: []CollectionFeature{
			{
				ID:          "go",
				Version:     "1.2.3",
				Name:        "Go",
				Description: "Installs Go and common tools",
			},
			{
				ID:          testFeatureNode,
				Version:     "2.0.0",
				Name:        "Node.js",
				Description: "Installs Node.js and npm",
				Options: map[string]any{
					"version": map[string]any{
						"type":    "string",
						"default": "lts",
					},
				},
			},
		},
	}

	s.pushCollectionImage("test/ns", &collection)

	result, err := FetchCollection(s.regHost, "test/ns")
	s.Require().NoError(err)
	s.Require().NotNil(result)
	s.Len(result.Features, 2)
	s.Equal("go", result.Features[0].ID)
	s.Equal("1.2.3", result.Features[0].Version)
	s.Equal("Go", result.Features[0].Name)
	s.Equal(testFeatureNode, result.Features[1].ID)
	s.Equal("2.0.0", result.Features[1].Version)
	s.NotNil(result.Features[1].Options)
}

func (s *CollectionTestSuite) TestFetchCollection_EmptyFeatures() {
	collection := Collection{Features: []CollectionFeature{}}
	s.pushCollectionImage("test/empty", &collection)

	result, err := FetchCollection(s.regHost, "test/empty")
	s.Require().NoError(err)
	s.Require().NotNil(result)
	s.Empty(result.Features)
}

func (s *CollectionTestSuite) TestFetchCollection_RegistryNotFound() {
	_, err := FetchCollection("localhost:1", "nonexistent/ns")
	s.Error(err)
	s.Contains(err.Error(), "pull collection")
}

func (s *CollectionTestSuite) TestFetchCollection_InvalidJSON() {
	s.pushRawCollectionImage("test/badjson", []byte("not valid json"))

	_, err := FetchCollection(s.regHost, "test/badjson")
	s.Error(err)
	s.Contains(err.Error(), "parse collection.json")
}

func (s *CollectionTestSuite) TestListCollectionFeatures() {
	collection := Collection{
		Features: []CollectionFeature{
			{ID: "rust", Version: testVersion1_0_0, Name: "Rust"},
			{ID: "python", Version: "3.0.0", Name: "Python"},
			{ID: "java", Version: "1.5.0", Name: "Java"},
		},
	}
	s.pushCollectionImage("test/list", &collection)

	features, err := ListCollectionFeatures(s.regHost, "test/list")
	s.Require().NoError(err)
	s.Len(features, 3)
	s.Equal("rust", features[0].ID)
	s.Equal("python", features[1].ID)
	s.Equal("java", features[2].ID)
}

func (s *CollectionTestSuite) TestFetchCollection_DeprecatedFeature() {
	collection := Collection{
		Features: []CollectionFeature{
			{
				ID:         "old-feature",
				Version:    "0.1.0",
				Name:       "Old Feature",
				Deprecated: true,
			},
		},
	}
	s.pushCollectionImage("test/deprecated", &collection)

	result, err := FetchCollection(s.regHost, "test/deprecated")
	s.Require().NoError(err)
	s.True(result.Features[0].Deprecated)
}

func (s *CollectionTestSuite) TestFetchCollection_FallbackToFirstLayer() {
	collection := Collection{
		Features: []CollectionFeature{
			{ID: "fallback", Version: testVersion1_0_0, Name: "Fallback"},
		},
	}
	data, err := json.Marshal(collection)
	s.Require().NoError(err)

	layer := static.NewLayer(data, types.OCILayer)
	img, err := mutate.AppendLayers(empty.Image, layer)
	s.Require().NoError(err)

	refStr := s.regHost + "/test/fallback/devcontainer-collection:latest"
	ref, err := name.ParseReference(refStr, name.Insecure)
	s.Require().NoError(err)
	s.Require().NoError(remote.Write(ref, img))

	result, err := FetchCollection(s.regHost, "test/fallback")
	s.Require().NoError(err)
	s.Len(result.Features, 1)
	s.Equal("fallback", result.Features[0].ID)
}

func (s *CollectionTestSuite) TestBuildCollectionRef() {
	ref, err := buildCollectionRef("ghcr.io", "devcontainers/features")
	s.Require().NoError(err)
	s.Equal("ghcr.io/devcontainers/features/devcontainer-collection:latest", ref.String())
}

func (s *CollectionTestSuite) TestParseCollection_ValidJSON() {
	input := `{"features":[{"id":"go","version":"1.0.0","name":"Go","description":"Go tools"}]}`
	r := strings.NewReader(input)

	collection, err := parseCollection(r)
	s.Require().NoError(err)
	s.Len(collection.Features, 1)
	s.Equal("go", collection.Features[0].ID)
	s.Equal("Go tools", collection.Features[0].Description)
}

func (s *CollectionTestSuite) TestParseCollection_EmptyObject() {
	r := strings.NewReader(`{}`)

	collection, err := parseCollection(r)
	s.Require().NoError(err)
	s.Nil(collection.Features)
}

func (s *CollectionTestSuite) pushCollectionImage(namespace string, collection *Collection) {
	s.T().Helper()

	data, err := json.Marshal(collection)
	s.Require().NoError(err)

	s.pushRawCollectionImage(namespace, data)
}

func (s *CollectionTestSuite) pushRawCollectionImage(namespace string, data []byte) {
	s.T().Helper()

	layer := static.NewLayer(data, types.MediaType(CollectionLayerMediaType))
	img, err := mutate.AppendLayers(empty.Image, layer)
	s.Require().NoError(err)

	img = setConfigMediaType(s.T(), img)

	refStr := s.regHost + "/" + namespace + "/devcontainer-collection:latest"
	ref, err := name.ParseReference(refStr, name.Insecure)
	s.Require().NoError(err)
	s.Require().NoError(remote.Write(ref, img))
}

func setConfigMediaType(t *testing.T, img v1.Image) v1.Image {
	t.Helper()

	cfg, err := img.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}

	img, err = mutate.ConfigFile(img, cfg)
	if err != nil {
		t.Fatal(err)
	}

	return img
}
