package collection

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/devcontainer/feature"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("features list-collection", ginkgo.Label("collection", "suite"), func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.It("lists features from a collection registry", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")

		srv := httptest.NewServer(registry.New())
		ginkgo.DeferCleanup(func() { srv.Close() })

		regHost := strings.TrimPrefix(srv.URL, "http://")

		col := feature.Collection{
			SourceInformation: feature.SourceInformation{
				Repository: "https://github.com/devcontainers/features",
				Type:       "github",
			},
			Features: []feature.CollectionFeature{
				{
					ID:          "go",
					Version:     "1.2.0",
					Name:        "Go",
					Description: "Installs Go and common Go tools",
				},
				{
					ID:          "node",
					Version:     "1.5.0",
					Name:        "Node.js",
					Description: "Installs Node.js, nvm, and yarn",
					Deprecated:  true,
				},
			},
		}

		pushCollectionImage(regHost, "devcontainers/features", col)

		stdout, err := f.ExecCommandOutput(ctx, []string{
			"features", "list-collection",
			regHost + "/devcontainers/features",
			"--output", "json",
		})
		framework.ExpectNoError(err)

		var features []feature.CollectionFeature
		err = json.Unmarshal([]byte(stdout), &features)
		framework.ExpectNoError(err)

		gomega.Expect(features).To(gomega.HaveLen(2))
		gomega.Expect(features[0].ID).To(gomega.Equal("go"))
		gomega.Expect(features[0].Version).To(gomega.Equal("1.2.0"))
		gomega.Expect(features[1].ID).To(gomega.Equal("node"))
		gomega.Expect(features[1].Deprecated).To(gomega.BeTrue())
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("returns error for non-existent collection", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")

		srv := httptest.NewServer(registry.New())
		ginkgo.DeferCleanup(func() { srv.Close() })

		regHost := strings.TrimPrefix(srv.URL, "http://")

		_, err := f.ExecCommandOutput(ctx, []string{
			"features", "list-collection",
			regHost + "/nonexistent/collection",
		})
		gomega.Expect(err).To(gomega.HaveOccurred())
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))
})

func pushCollectionImage(regHost, namespace string, col feature.Collection) {
	raw, err := json.Marshal(col)
	framework.ExpectNoError(err)

	layer := static.NewLayer(raw, types.MediaType(feature.CollectionLayerMediaType))
	img, err := mutate.AppendLayers(empty.Image, layer)
	framework.ExpectNoError(err)

	refStr := regHost + "/" + namespace + "/devcontainer-collection:latest"
	ref, err := name.ParseReference(refStr, name.Insecure)
	framework.ExpectNoError(err)

	framework.ExpectNoError(remote.Write(ref, img))
}
