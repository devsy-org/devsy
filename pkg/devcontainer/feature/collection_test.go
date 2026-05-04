package feature

import (
	"bytes"
	"encoding/json"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/stretchr/testify/suite"
)

const testFeatureNode = "node"

type CollectionTestSuite struct {
	suite.Suite
}

func TestCollectionTestSuite(t *testing.T) {
	suite.Run(t, new(CollectionTestSuite))
}

func (s *CollectionTestSuite) TestParseCollection_HappyPath() {
	col := Collection{
		SourceInformation: SourceInformation{
			Repository: "https://github.com/devcontainers/features",
			Type:       "github",
		},
		Features: []CollectionFeature{
			{
				ID:          "go",
				Version:     "1.2.0",
				Name:        "Go",
				Description: "Installs Go and common Go tools",
			},
			{
				ID:          testFeatureNode,
				Version:     "1.5.0",
				Name:        "Node.js",
				Description: "Installs Node.js, nvm, and yarn",
				Deprecated:  true,
			},
		},
	}

	raw, err := json.Marshal(col)
	s.Require().NoError(err)

	parsed, err := parseCollection(bytes.NewReader(raw))
	s.Require().NoError(err)
	s.Equal("https://github.com/devcontainers/features", parsed.SourceInformation.Repository)
	s.Equal("github", parsed.SourceInformation.Type)
	s.Len(parsed.Features, 2)
	s.Equal("go", parsed.Features[0].ID)
	s.Equal("1.2.0", parsed.Features[0].Version)
	s.Equal("Go", parsed.Features[0].Name)
	s.Equal("Installs Go and common Go tools", parsed.Features[0].Description)
	s.False(parsed.Features[0].Deprecated)
	s.Equal(testFeatureNode, parsed.Features[1].ID)
	s.True(parsed.Features[1].Deprecated)
}

func (s *CollectionTestSuite) TestParseCollection_EmptyFeatures() {
	col := Collection{Features: []CollectionFeature{}}
	raw, err := json.Marshal(col)
	s.Require().NoError(err)

	parsed, err := parseCollection(bytes.NewReader(raw))
	s.Require().NoError(err)
	s.Empty(parsed.Features)
}

func (s *CollectionTestSuite) TestParseCollection_InvalidJSON() {
	_, err := parseCollection(bytes.NewReader([]byte("not json")))
	s.Error(err)
	s.Contains(err.Error(), "parse collection JSON")
}

func (s *CollectionTestSuite) TestParseCollection_WithOptions() {
	col := Collection{
		Features: []CollectionFeature{
			{
				ID:      "python",
				Version: "2.0.0",
				Name:    "Python",
				Options: map[string]FeatureOptionEntry{
					"version": {
						Type:        "string",
						Default:     "3.11",
						Description: "Python version to install",
						Proposals:   []string{"3.10", "3.11", "3.12"},
					},
				},
			},
		},
	}

	raw, err := json.Marshal(col)
	s.Require().NoError(err)

	parsed, err := parseCollection(bytes.NewReader(raw))
	s.Require().NoError(err)
	s.Len(parsed.Features, 1)
	s.Contains(parsed.Features[0].Options, "version")
	s.Equal("string", parsed.Features[0].Options["version"].Type)
	s.Equal("3.11", parsed.Features[0].Options["version"].Default)
}

func (s *CollectionTestSuite) TestExtractCollectionJSON_TypedLayer() {
	col := Collection{
		Features: []CollectionFeature{
			{ID: "rust", Version: "1.0.0", Name: "Rust"},
		},
	}
	raw, err := json.Marshal(col)
	s.Require().NoError(err)

	layer := static.NewLayer(raw, types.MediaType(CollectionLayerMediaType))
	img, err := mutate.AppendLayers(empty.Image, layer)
	s.Require().NoError(err)

	parsed, err := extractCollectionJSON(img)
	s.Require().NoError(err)
	s.Len(parsed.Features, 1)
	s.Equal("rust", parsed.Features[0].ID)
}

func (s *CollectionTestSuite) TestExtractCollectionJSON_FallbackToFirstLayer() {
	col := Collection{
		Features: []CollectionFeature{
			{ID: "java", Version: "2.1.0", Name: "Java"},
		},
	}
	raw, err := json.Marshal(col)
	s.Require().NoError(err)

	layer := static.NewLayer(raw, types.OCIUncompressedLayer)
	img, err := mutate.AppendLayers(empty.Image, layer)
	s.Require().NoError(err)

	parsed, err := extractCollectionJSON(img)
	s.Require().NoError(err)
	s.Len(parsed.Features, 1)
	s.Equal("java", parsed.Features[0].ID)
}

func (s *CollectionTestSuite) TestFindCollectionLayer_NoLayers() {
	_, err := findCollectionLayer(empty.Image)
	s.Error(err)
	s.Contains(err.Error(), "no layers")
}

func (s *CollectionTestSuite) TestBuildCollectionRef() {
	ref, err := buildCollectionRef("ghcr.io", "devcontainers/features")
	s.Require().NoError(err)
	s.Equal("ghcr.io/devcontainers/features/devcontainer-collection:latest", ref.String())
}

func (s *CollectionTestSuite) TestBuildCollectionRef_InvalidRegistry() {
	_, err := buildCollectionRef("", "INVALID@@@REF")
	s.Error(err)
}

func (s *CollectionTestSuite) TestListCollectionFeatures_FromImage() {
	col := Collection{
		Features: []CollectionFeature{
			{ID: "go", Version: "1.2.0", Name: "Go", Description: "Go language"},
			{ID: testFeatureNode, Version: "1.5.0", Name: "Node.js", Description: "Node runtime"},
		},
	}
	raw, err := json.Marshal(col)
	s.Require().NoError(err)

	layer := static.NewLayer(raw, types.MediaType(CollectionLayerMediaType))
	img, err := mutate.AppendLayers(empty.Image, layer)
	s.Require().NoError(err)

	parsed, err := extractCollectionJSON(img)
	s.Require().NoError(err)
	s.Len(parsed.Features, 2)
	s.Equal("go", parsed.Features[0].ID)
	s.Equal(testFeatureNode, parsed.Features[1].ID)
}

func (s *CollectionTestSuite) TestFindCollectionLayer_PrefersTypedLayer() {
	untyped := static.NewLayer([]byte("garbage"), types.OCIUncompressedLayer)

	col := Collection{Features: []CollectionFeature{{ID: "correct"}}}
	raw, _ := json.Marshal(col)
	typed := static.NewLayer(raw, types.MediaType(CollectionLayerMediaType))

	img, err := mutate.AppendLayers(empty.Image, untyped, typed)
	s.Require().NoError(err)

	layer, err := findCollectionLayer(img)
	s.Require().NoError(err)

	mt, err := layer.MediaType()
	s.Require().NoError(err)
	s.Equal(types.MediaType(CollectionLayerMediaType), mt)

	// Verify the layer content is the typed one
	data, err := layer.Uncompressed()
	s.Require().NoError(err)
	defer func() { _ = data.Close() }()

	var result Collection
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(data)
	_ = json.Unmarshal(buf.Bytes(), &result)
	s.Equal("correct", result.Features[0].ID)
}

func (s *CollectionTestSuite) TestExtractCollectionJSON_EmptyManifest() {
	img := emptyImageWithLayers(0)
	_, err := extractCollectionJSON(img)
	s.Error(err)
}

func emptyImageWithLayers(n int) v1.Image {
	if n == 0 {
		return empty.Image
	}
	img := empty.Image
	for range n {
		layer := static.NewLayer([]byte("{}"), types.OCIUncompressedLayer)
		img, _ = mutate.AppendLayers(img, layer)
	}
	return img
}
