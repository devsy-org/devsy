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

type CollectionFeature struct {
	ID               string         `json:"id"`
	Version          string         `json:"version"`
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	DocumentationURL string         `json:"documentationURL,omitempty"`
	Options          map[string]any `json:"options,omitempty"`
	Deprecated       bool           `json:"deprecated,omitempty"`
}

type Collection struct {
	Features []CollectionFeature `json:"features"`
}

func FetchCollection(registry, namespace string) (*Collection, error) {
	ref, err := buildCollectionRef(registry, namespace)
	if err != nil {
		return nil, fmt.Errorf("parse collection reference: %w", err)
	}

	log.Debugf("fetching collection.json: registry=%s, namespace=%s", registry, namespace)

	img, err := pullCollectionImage(ref)
	if err != nil {
		return nil, err
	}

	return extractCollectionJSON(img)
}

func ListCollectionFeatures(registry, namespace string) ([]CollectionFeature, error) {
	collection, err := FetchCollection(registry, namespace)
	if err != nil {
		return nil, err
	}
	return collection.Features, nil
}

func buildCollectionRef(registry, namespace string) (name.Reference, error) {
	refStr := fmt.Sprintf("%s/%s/devcontainer-collection:latest", registry, namespace)
	return name.ParseReference(refStr)
}

func pullCollectionImage(ref name.Reference) (v1.Image, error) {
	var img v1.Image
	err := retryOCIPull(func() error {
		log.Debugf("fetching collection OCI image: reference=%s", ref.String())
		var fetchErr error
		img, fetchErr = remote.Image(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain))
		return fetchErr
	})
	if err != nil {
		err = image.SanitizeRegistryError(err)
		registry := sanitizeURL(ref.Context().RegistryStr())
		log.Debugf("failed to fetch collection image: error=%v, registry=%s", err, registry)
		return nil, fmt.Errorf("pull collection from %s: %w", registry, err)
	}
	return img, nil
}

func extractCollectionJSON(img v1.Image) (*Collection, error) {
	layer, err := findCollectionLayer(img)
	if err != nil {
		return nil, err
	}

	data, err := layer.Uncompressed()
	if err != nil {
		return nil, fmt.Errorf("read collection layer: %w", err)
	}
	defer func() { _ = data.Close() }()

	return parseCollection(data)
}

func findCollectionLayer(img v1.Image) (v1.Layer, error) {
	manifest, err := img.Manifest()
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	for _, desc := range manifest.Layers {
		if string(desc.MediaType) == CollectionLayerMediaType {
			layer, err := img.LayerByDigest(desc.Digest)
			if err != nil {
				return nil, fmt.Errorf("retrieve collection layer: %w", err)
			}
			return layer, nil
		}
	}

	if len(manifest.Layers) == 0 {
		return nil, fmt.Errorf("collection image has no layers")
	}

	log.Debugf(
		"no layer with media type %s found, falling back to first layer",
		CollectionLayerMediaType,
	)
	layer, err := img.LayerByDigest(manifest.Layers[0].Digest)
	if err != nil {
		return nil, fmt.Errorf("retrieve first layer: %w", err)
	}
	return layer, nil
}

func parseCollection(r io.Reader) (*Collection, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read collection data: %w", err)
	}

	var collection Collection
	if err := json.Unmarshal(raw, &collection); err != nil {
		return nil, fmt.Errorf("parse collection.json: %w", err)
	}

	log.Debugf("parsed collection: %d features found", len(collection.Features))
	return &collection, nil
}
