package framework

import (
	"archive/tar"
	"bytes"
	"compress/gzip"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/onsi/gomega"
)

// BuildFeatureTarGz returns a gzipped tar holding a single file.
func BuildFeatureTarGz(filename, content string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	hdr := &tar.Header{
		Name: filename,
		Mode: 0o644,
		Size: int64(len(content)),
	}
	gomega.Expect(tw.WriteHeader(hdr)).To(gomega.Succeed())
	_, err := tw.Write([]byte(content))
	ExpectNoError(err)
	gomega.Expect(tw.Close()).To(gomega.Succeed())
	gomega.Expect(gz.Close()).To(gomega.Succeed())
	return buf.Bytes()
}

// PushFeatureTag pushes a minimal my-feature@1.0.0 OCI feature to refStr.
func PushFeatureTag(refStr string) {
	layer := static.NewLayer(
		BuildFeatureTarGz("devcontainer-feature.json", `{"id":"my-feature","version":"1.0.0"}`),
		types.OCILayer,
	)

	img, err := mutate.AppendLayers(empty.Image, layer)
	ExpectNoError(err)

	ref, err := name.ParseReference(refStr, name.Insecure)
	ExpectNoError(err)

	ExpectNoError(remote.Write(ref, img))
}
