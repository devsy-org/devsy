package image

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/devsy-org/devsy/pkg/extract"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/stream"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

func ResolvePublishVersion(version string) string {
	if version == "" {
		return "latest"
	}

	return version
}

func ValidatePublishDir(target string) error {
	stat, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("stat target: %w", err)
	}

	if !stat.IsDir() {
		return fmt.Errorf("target must be a directory: %s", target)
	}

	return nil
}

func ParsePublishReference(registry, namespace, id, version string) (name.Reference, error) {
	refStr := buildPublishReference(registry, namespace, id, version)

	ref, err := name.ParseReference(refStr)
	if err != nil {
		return nil, fmt.Errorf("parse publish reference %q: %w", refStr, err)
	}

	return ref, nil
}

func buildPublishReference(registry, namespace, id, version string) string {
	if namespace != "" {
		return fmt.Sprintf("%s/%s/%s:%s", registry, namespace, id, version)
	}

	return fmt.Sprintf("%s/%s:%s", registry, id, version)
}

func BuildPublishImage(kind, sourceDir string) (v1.Image, error) {
	var buf bytes.Buffer
	if err := extract.WriteTar(&buf, sourceDir, true); err != nil {
		return nil, fmt.Errorf("create %s archive: %w", kind, err)
	}

	layer := stream.NewLayer(
		io.NopCloser(bytes.NewReader(buf.Bytes())),
		stream.WithMediaType(types.OCILayer),
	)

	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		return nil, fmt.Errorf("build %s image: %w", kind, err)
	}

	return img, nil
}

func PushPublishImage(kind string, ref name.Reference, img v1.Image) error {
	if err := remote.Write(
		ref, img, remote.WithAuthFromKeychain(authn.DefaultKeychain),
	); err != nil {
		return fmt.Errorf("push %s to registry: %w", kind, err)
	}

	log.Infof("%s published successfully: %s", kind, ref.String())

	return nil
}

func WritePublishedMetadata(w io.Writer, metadata any) error {
	metadataJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal published metadata: %w", err)
	}

	_, _ = w.Write(metadataJSON)
	_, _ = io.WriteString(w, "\n")

	return nil
}
