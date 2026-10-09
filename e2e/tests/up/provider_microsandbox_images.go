package up

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/image"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const microsandboxImageFixture = "tests/up/testdata/microsandbox-images"

var _ = ginkgo.Describe("microsandbox image backend parity",
	ginkgo.Label("up-provider-microsandbox-images"), func() {
		for _, provider := range []struct{ name, source string }{
			{"builtin", "microsandbox"},
			{"external", microsandboxExternalProvider},
		} {
			ginkgo.Context(provider.name, func() {
				var f *framework.Framework
				var tempDir string

				ginkgo.BeforeEach(func(ctx context.Context) {
					skipIfNoMicrosandbox(ctx)
					ginkgo.GinkgoT().Setenv("DEVSY_HOME", ginkgo.GinkgoT().TempDir())
					ginkgo.GinkgoT().Setenv("DEVSY_CONFIG", "")
					initialDir, err := os.Getwd()
					framework.ExpectNoError(err)
					f = framework.NewDefaultFramework(filepath.Join(initialDir, "bin"))
					tempDir, err = framework.CopyToTempDir(microsandboxImageFixture)
					framework.ExpectNoError(err)
					ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
					framework.ExpectNoError(
						f.DevsyProviderAdd(ctx, provider.source, "--name", provider.name),
					)
					ginkgo.DeferCleanup(f.DevsyProviderDelete, provider.name)
					ginkgo.DeferCleanup(f.CleanupWorkspace, tempDir)
				})

				ginkgo.It(
					"falls back to the registry when the image is absent from Docker",
					func(ctx context.Context) {
						ref, manifestReads := microsandboxRegistryImage(ctx)
						assertMicrosandboxImageAbsentFromDocker(ctx, ref)
						writeMicrosandboxImageConfig(tempDir, ref)
						framework.ExpectNoError(
							f.DevsyUp(ctx, tempDir, "--devcontainer", ".devcontainer.json"),
						)
						out, err := f.DevsySSHOnce(ctx, tempDir, "grep '^ID=' /etc/os-release")
						framework.ExpectNoError(err)
						gomega.Expect(out).To(gomega.Equal("ID=alpine\n"))
						gomega.Expect(manifestReads.Load()).To(gomega.BeNumerically(">", 0))
						assertMicrosandboxImageAbsentFromDocker(ctx, ref)
					},
					ginkgo.SpecTimeout(framework.TimeoutLong()),
				)

				ginkgo.It(
					"loads a Docker-only image without a registry copy",
					func(ctx context.Context) {
						ref := "devsy-msb-local:" + filepath.Base(tempDir)
						microsandboxDocker(
							ctx,
							"build",
							"--platform",
							"linux/"+runtime.GOARCH,
							"-t",
							ref,
							tempDir,
						)
						ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
							microsandboxDocker(cleanupCtx, "image", "rm", ref)
						})
						writeMicrosandboxImageConfig(tempDir, ref)
						framework.ExpectNoError(
							f.DevsyUp(ctx, tempDir, "--devcontainer", ".devcontainer.json"),
						)
						assertMicrosandboxImageMarker(ctx, f, tempDir)
					},
					ginkgo.SpecTimeout(framework.TimeoutLong()),
				)

				ginkgo.It(
					"builds a Dockerfile and installs a local Dev Container Feature",
					func(ctx context.Context) {
						framework.ExpectNoError(
							f.DevsyUp(ctx, tempDir, "--devcontainer", ".devcontainer.json"),
						)
						assertMicrosandboxImageMarker(ctx, f, tempDir)
						out, err := f.DevsySSHOnce(
							ctx,
							tempDir,
							"cat /usr/local/share/devsy-feature-parity; printf '%s\\n' \"$DEVSY_FEATURE_PARITY\"",
						)
						framework.ExpectNoError(err)
						gomega.Expect(out).To(gomega.Equal("feature-parity\nfeature-parity\n"))
					},
					ginkgo.SpecTimeout(framework.TimeoutLong()),
				)
			})
		}
	})

func microsandboxRegistryImage(ctx context.Context) (string, *atomic.Int64) {
	var manifestReads atomic.Int64
	handler := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/manifests/") {
			manifestReads.Add(1)
		}
		handler.ServeHTTP(w, r)
	}))
	ginkgo.DeferCleanup(server.Close)
	ref, err := name.NewTag(server.Listener.Addr().String() + "/parity:registry")
	framework.ExpectNoError(err)
	img, err := image.GetImageForArch(
		ctx,
		"ghcr.io/devsy-org/test-images/base:alpine",
		runtime.GOARCH,
	)
	framework.ExpectNoError(err)
	framework.ExpectNoError(remote.Write(ref, img, remote.WithContext(ctx)))
	manifestReads.Store(0)
	return ref.Name(), &manifestReads
}

func writeMicrosandboxImageConfig(dir, ref string) {
	data, err := json.Marshal(map[string]string{
		"image": ref, "containerUser": microsandboxRootUser, "remoteUser": microsandboxRootUser,
	})
	framework.ExpectNoError(err)
	framework.ExpectNoError(os.WriteFile(filepath.Join(dir, ".devcontainer.json"), data, 0o600))
}

func assertMicrosandboxImageMarker(ctx context.Context, f *framework.Framework, workspace string) {
	out, err := f.DevsySSHOnce(ctx, workspace,
		"cat /usr/local/share/devsy-image-parity; printf '%s\\n' \"$DEVSY_IMAGE_PARITY\"")
	framework.ExpectNoError(err)
	gomega.Expect(out).To(gomega.Equal("dockerfile-parity\ndockerfile-parity\n"))
}

func assertMicrosandboxImageAbsentFromDocker(ctx context.Context, ref string) {
	local := microsandboxDocker(ctx, "image", "ls", "--format", "{{.Repository}}:{{.Tag}}", ref)
	gomega.Expect(strings.TrimSpace(local)).To(gomega.BeEmpty())
}

func microsandboxDocker(ctx context.Context, args ...string) string {
	// #nosec G204 -- fixed Docker executable and controlled image fixture arguments.
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "docker %v: %s", args, out)
	return string(out)
}
