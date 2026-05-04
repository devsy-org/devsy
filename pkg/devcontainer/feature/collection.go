package feature

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/devsy-org/devsy/pkg/image"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

const CollectionLayerMediaType = "application/vnd.devcontainers.collection.layer.v1+json"

type Collection struct {
	SourceInformation SourceInformation   `json:"sourceInformation,omitzero"`
	Features          []CollectionFeature `json:"features"`
}

type SourceInformation struct {
	Repository string `json:"repository,omitempty"`
	Type       string `json:"type,omitempty"`
}

type CollectionFeature struct {
	ID               string                        `json:"id"`
	Version          string                        `json:"version"`
	Name             string                        `json:"name"`
	Description      string                        `json:"description"`
	DocumentationURL string                        `json:"documentationURL,omitempty"`
	Options          map[string]FeatureOptionEntry `json:"options,omitempty"`
	Deprecated       bool                          `json:"deprecated,omitempty"`
}

type FeatureOptionEntry struct {
	Type        string   `json:"type,omitempty"`
	Default     any      `json:"default,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Proposals   []string `json:"proposals,omitempty"`
}

func FetchCollectionJSON(registry, namespace string) (*Collection, error) {
	ref, err := buildCollectionRef(registry, namespace)
	if err != nil {
		return nil, fmt.Errorf("parse collection reference: %w", err)
	}

	img, err := pullCollectionImage(ref)
	if err != nil {
		return nil, err
	}

	return extractCollectionJSON(img)
}

func ListCollectionFeatures(registry, namespace string) ([]CollectionFeature, error) {
	col, err := FetchCollectionJSON(registry, namespace)
	if err != nil {
		return nil, err
	}
	return col.Features, nil
}

func buildCollectionRef(registry, namespace string) (name.Reference, error) {
	refStr := fmt.Sprintf("%s/%s/devcontainer-collection:latest", registry, namespace)
	return name.ParseReference(refStr)
}

func pullCollectionImage(ref name.Reference) (v1.Image, error) {
	var img v1.Image
	err := retryOCIPull(func() error {
		log.Debugf("fetching collection image: reference=%s", ref.String())
		var fetchErr error
		img, fetchErr = remote.Image(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain))
		return fetchErr
	})
	if err != nil {
		err = image.SanitizeRegistryError(err)
		registry := sanitizeURL(ref.Context().RegistryStr())
		return nil, fmt.Errorf("pull collection from %s: %w", registry, err)
	}
	return img, nil
}

func extractCollectionJSON(img v1.Image) (*Collection, error) {
	layer, err := findCollectionLayer(img)
	if err != nil {
		return nil, fmt.Errorf("find collection layer: %w", err)
	}

	data, err := layer.Uncompressed()
	if err != nil {
		return nil, fmt.Errorf("uncompress collection layer: %w", err)
	}
	defer func() { _ = data.Close() }()

	return parseCollection(data)
}

func findCollectionLayer(img v1.Image) (v1.Layer, error) {
	manifest, err := img.Manifest()
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	if len(manifest.Layers) == 0 {
		return nil, fmt.Errorf("image has no layers")
	}

	for _, desc := range manifest.Layers {
		if string(desc.MediaType) == CollectionLayerMediaType {
			return img.LayerByDigest(desc.Digest)
		}
	}

	return img.LayerByDigest(manifest.Layers[0].Digest)
}

func parseCollection(r io.Reader) (*Collection, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read collection data: %w", err)
	}

	var col Collection
	if err := json.Unmarshal(raw, &col); err != nil {
		return nil, fmt.Errorf("parse collection JSON: %w", err)
	}

	return &col, nil
}
