package up

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const microsandboxPrebuildFixture = "tests/up/testdata/microsandbox-prebuilds"

var _ = ginkgo.Describe("microsandbox prebuild parity",
	ginkgo.Label("up-provider-microsandbox-prebuilds"), func() {
		for _, provider := range microsandboxProviders("prebuild-builtin", "prebuild-external") {
			ginkgo.Context(provider.name, ginkgo.Label(provider.label), func() {
				ginkgo.DescribeTable(
					"selects prebuilds by input hash",
					func(ctx context.Context, changed bool) {
						fixture := newMicrosandboxPrebuildFixture(
							ctx,
							provider.name,
							provider.source,
						)
						publishedRef := fixture.publish(ctx)
						fixture.consume(ctx, publishedRef, changed)
					},
					ginkgo.Entry(
						"reuses a matching Dockerfile and Feature without rebuilding",
						false,
						ginkgo.SpecTimeout(framework.TimeoutLong()),
					),
					ginkgo.Entry(
						"builds changed Dockerfile input instead of selecting the old prebuild",
						true,
						ginkgo.SpecTimeout(framework.TimeoutLong()),
					),
				)
			})
		}
	})

type microsandboxPrebuildTest struct {
	framework *framework.Framework
	workspace string
	repo      string
	server    *httptest.Server
	registry  *microsandboxPrebuildRegistry
	counter   *microsandboxBuildCounter
}

func newMicrosandboxPrebuildFixture(
	ctx context.Context,
	providerName, providerSource string,
) *microsandboxPrebuildTest {
	// The test process and local Docker daemon must share host loopback.
	framework.SkipUnlessOS("linux", "requires the Linux CI host's local Docker daemon")
	skipIfNoMicrosandbox(ctx)
	ginkgo.GinkgoT().Setenv("DEVSY_HOME", ginkgo.GinkgoT().TempDir())
	ginkgo.GinkgoT().Setenv("DEVSY_CONFIG", "")
	initialDir, err := os.Getwd()
	framework.ExpectNoError(err)
	workspace, err := framework.CopyToTempDir(microsandboxPrebuildFixture)
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, workspace)
	fixture := &microsandboxPrebuildTest{
		framework: framework.NewDefaultFramework(filepath.Join(initialDir, "bin")),
		workspace: workspace,
		counter:   installMicrosandboxBuildCounter(ctx),
		registry:  &microsandboxPrebuildRegistry{},
	}
	backend := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	fixture.server = httptest.NewServer(fixture.registry.handler(backend))
	fixture.repo = fixture.server.Listener.Addr().String() + "/" + filepath.Base(workspace)
	ginkgo.DeferCleanup(fixture.shutdownRegistry)
	ginkgo.DeferCleanup(func() error {
		return fixture.counter.cleanupTags(fixture.registry.publishedRefs(fixture.repo))
	})
	framework.ExpectNoError(
		fixture.framework.DevsyProviderAdd(ctx, providerSource, "--name", providerName),
	)
	ginkgo.DeferCleanup(func() error {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		return fixture.framework.DevsyProviderDelete(cleanupCtx, providerName)
	})
	ginkgo.DeferCleanup(func() error {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		return fixture.framework.DevsyWorkspaceDelete(cleanupCtx, workspace)
	})
	return fixture
}

func (f *microsandboxPrebuildTest) shutdownRegistry() error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := f.server.Config.Shutdown(shutdownCtx)
	// Force-close connections after graceful shutdown, preserving timeout errors.
	closeErr := f.server.Config.Close()
	f.server.Client().CloseIdleConnections()
	return errors.Join(shutdownErr, closeErr)
}

func (f *microsandboxPrebuildTest) publish(ctx context.Context) string {
	writeMicrosandboxPrebuildConfig(f.workspace, f.repo)
	writeMicrosandboxPrebuildDockerfile(f.workspace, filepath.Base(f.workspace)+"-published")
	framework.ExpectNoError(f.framework.DevsyBuild(ctx, f.workspace,
		"--repository", f.repo, "--devcontainer-path", ".devcontainer.json"))
	gomega.Expect(f.counter.count()).To(gomega.BeNumerically(">", 0),
		"publication must exercise the instrumented host Docker builder")
	published := f.registry.publishedRefs(f.repo)
	gomega.Expect(published).To(gomega.HaveLen(1))
	oldRef := published[0]
	for tag := range uniqueMicrosandboxPrebuildTags(append(f.counter.tags(), oldRef)) {
		out, err := f.counter.command(ctx, "image", "rm", tag)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "%s", out)
	}
	f.counter.resetCounts()
	f.registry.resetReads()
	return oldRef
}

func (f *microsandboxPrebuildTest) consume(ctx context.Context, oldRef string, changed bool) {
	marker := filepath.Base(f.workspace) + "-published"
	if changed {
		marker = filepath.Base(f.workspace) + "-changed"
		writeMicrosandboxPrebuildDockerfile(f.workspace, marker)
	}
	framework.ExpectNoError(
		f.framework.DevsyUp(ctx, f.workspace, "--devcontainer", ".devcontainer.json"),
	)
	if changed {
		gomega.Expect(f.counter.count()).To(gomega.BeNumerically(">", 0))
		gomega.Expect(f.registry.missedDifferentHash(oldRef)).To(gomega.BeTrue())
		// Probe after consumption assertions: this GET cannot prove Devsy lookup.
		oldTag := strings.TrimPrefix(oldRef, f.repo+":")
		assertMicrosandboxPrebuildStillPublished(ctx, f.server.Client(),
			f.server.URL+"/v2/"+filepath.Base(f.workspace)+"/manifests/"+oldTag)
	} else {
		gomega.Expect(f.counter.count()).To(gomega.Equal(0))
		gomega.Expect(f.counter.probeCount()).To(gomega.Equal(0),
			"matching prebuild reuse must not enter build strategy selection")
		gomega.Expect(f.registry.readPublishedHash(oldRef)).To(gomega.BeTrue())
	}
	command := "cat /usr/local/share/devsy-prebuild-marker; " +
		"cat /usr/local/share/devsy-feature-parity; printf '%s\\n' \"$DEVSY_FEATURE_PARITY\""
	out, err := f.framework.DevsySSHOnce(ctx, f.workspace, command)
	framework.ExpectNoError(err)
	gomega.Expect(out).To(gomega.Equal(marker + "\nfeature-parity\nfeature-parity\n"))
}

func assertMicrosandboxPrebuildStillPublished(
	ctx context.Context,
	client *http.Client,
	manifestURL string,
) {
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, manifestURL, nil)
	framework.ExpectNoError(err)
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, "+
		"application/vnd.docker.distribution.manifest.v2+json")
	response, err := client.Do(req)
	framework.ExpectNoError(err)
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	framework.ExpectNoError(errors.Join(readErr, closeErr))
	gomega.Expect(response.StatusCode).To(gomega.Equal(http.StatusOK),
		"the old prebuild must remain available after changed-input consumption")
}

func writeMicrosandboxPrebuildConfig(dir, repo string) {
	data, err := json.Marshal(map[string]any{
		"build":         map[string]string{"dockerfile": "Dockerfile"},
		"containerUser": "root",
		"remoteUser":    "root",
		"features":      map[string]any{"./features/parity": map[string]any{}},
		"customizations": map[string]any{
			"devsy": map[string]any{"prebuildRepository": []string{repo}},
		},
	})
	framework.ExpectNoError(err)
	framework.ExpectNoError(os.WriteFile(filepath.Join(dir, ".devcontainer.json"), data, 0o600))
}

func writeMicrosandboxPrebuildDockerfile(dir, marker string) {
	content := "FROM ghcr.io/devsy-org/test-images/base:alpine\nRUN mkdir -p /usr/local/share && printf '%s\\n' " +
		microsandboxPrebuildShellQuote(
			marker,
		) + " > /usr/local/share/devsy-prebuild-marker\n"
	framework.ExpectNoError(os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(content), 0o600))
}

type microsandboxBuildCounter struct{ realDocker, countFile, probeFile, tagFile string }

func installMicrosandboxBuildCounter(ctx context.Context) *microsandboxBuildCounter {
	realDocker, err := exec.LookPath("docker")
	framework.ExpectNoError(err)
	realDocker, err = filepath.Abs(realDocker)
	framework.ExpectNoError(err)
	dir := ginkgo.GinkgoT().TempDir()
	c := &microsandboxBuildCounter{
		realDocker: realDocker,
		countFile:  filepath.Join(dir, "builds"),
		probeFile:  filepath.Join(dir, "strategy-probes"),
		tagFile:    filepath.Join(dir, "tags"),
	}
	c.resetCounts()
	framework.ExpectNoError(os.WriteFile(c.tagFile, nil, 0o600))
	// Devsy supplies buildx/build as the first arguments; context/host selection
	// remains in the inherited environment. Never rewrite or consume argv.
	script := "#!/bin/sh\nset -eu\ncase \"${1-}:${2-}\" in\n  buildx:version)\n" +
		"    printf 'probe\\n' >> " + microsandboxPrebuildShellQuote(c.probeFile) + "\n  ;;\n" +
		"  build:*|buildx:build|image:build)\n" +
		"    printf 'build\\n' >> " + microsandboxPrebuildShellQuote(c.countFile) + "\n" +
		"    previous=''\n    for arg do\n      if [ \"$previous\" = '-t' ] || [ \"$previous\" = '--tag' ]; then\n" +
		"        printf '%s\\n' \"$arg\" >> " + microsandboxPrebuildShellQuote(c.tagFile) + "\n      fi\n" +
		"      case \"$arg\" in --tag=*) printf '%s\\n' \"${arg#--tag=}\" >> " + microsandboxPrebuildShellQuote(c.tagFile) +
		";; esac\n" +
		"      previous=$arg\n    done\n  ;;\nesac\nexec " + microsandboxPrebuildShellQuote(realDocker) + " \"$@\"\n"
	// #nosec G306 -- private executable wrapper must be executable by its owner.
	framework.ExpectNoError(os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o700))
	ginkgo.GinkgoT().Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := c.command(ctx, "buildx", "version")
	gomega.Expect(err).
		NotTo(gomega.HaveOccurred(), "buildx is required for observable build invocation: %s", out)
	return c
}

func (c *microsandboxBuildCounter) command(ctx context.Context, args ...string) (string, error) {
	// #nosec G204 -- captured Docker executable and controlled fixture arguments.
	cmd := exec.CommandContext(ctx, c.realDocker, args...)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (c *microsandboxBuildCounter) count() int {
	data, err := os.ReadFile(c.countFile)
	framework.ExpectNoError(err)
	return strings.Count(string(data), "build\n")
}

func (c *microsandboxBuildCounter) probeCount() int {
	data, err := os.ReadFile(c.probeFile)
	framework.ExpectNoError(err)
	return strings.Count(string(data), "probe\n")
}

func (c *microsandboxBuildCounter) resetCounts() {
	framework.ExpectNoError(os.WriteFile(c.countFile, nil, 0o600))
	framework.ExpectNoError(os.WriteFile(c.probeFile, nil, 0o600))
}

func (c *microsandboxBuildCounter) tags() []string {
	data, err := os.ReadFile(c.tagFile)
	framework.ExpectNoError(err)
	return strings.Fields(string(data))
}

func (c *microsandboxBuildCounter) cleanupTags(published []string) error {
	data, readErr := os.ReadFile(c.tagFile)
	var cleanupErrors []error
	if readErr != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("read owned build tags: %w", readErr))
	}
	owned := append(strings.Fields(string(data)), published...)
	for tag := range uniqueMicrosandboxPrebuildTags(owned) {
		if err := c.cleanupTag(tag); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func (c *microsandboxBuildCounter) cleanupTag(tag string) error {
	// A failed or timed-out tag must not consume another tag's cleanup budget.
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := c.command(cleanupCtx, "image", "ls", "--format", "{{.Repository}}:{{.Tag}}", tag)
	if err != nil {
		return fmt.Errorf("inspect owned image tag %q: %w (output: %s)", tag, err, out)
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}
	out, err = c.command(cleanupCtx, "image", "rm", tag)
	if err != nil {
		return fmt.Errorf("remove owned image tag %q: %w (output: %s)", tag, err, out)
	}
	return nil
}

func microsandboxPrebuildShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func uniqueMicrosandboxPrebuildTags(tags []string) map[string]struct{} {
	result := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		result[tag] = struct{}{}
	}
	return result
}

type microsandboxManifestRead struct {
	tag    string
	status int
}
type microsandboxPrebuildRegistry struct {
	mu        sync.Mutex
	published map[string]struct{}
	reads     []microsandboxManifestRead
}

type microsandboxRegistryStatus struct {
	http.ResponseWriter
	status        int
	onFinalStatus func(int)
}

func (w *microsandboxRegistryStatus) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	if status < 100 || status > 999 {
		// Preserve the underlying writer's invalid-status panic semantics.
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	// Account before bytes can reach the client, which can finish before the
	// handler returns. A mutex around post-response accounting is insufficient.
	w.onFinalStatus(status)
	w.ResponseWriter.WriteHeader(status)
}

func (w *microsandboxRegistryStatus) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (o *microsandboxPrebuildRegistry) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, tag, found := strings.Cut(r.URL.Path, "/manifests/")
		if !found || !strings.HasPrefix(tag, "devsy-") {
			next.ServeHTTP(w, r)
			return
		}
		capture := &microsandboxRegistryStatus{
			ResponseWriter: w,
			onFinalStatus:  func(status int) { o.record(r.Method, tag, status) },
		}
		next.ServeHTTP(capture, r)
		if capture.status == 0 {
			capture.WriteHeader(http.StatusOK)
		}
	})
}

func (o *microsandboxPrebuildRegistry) record(method, tag string, status int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if method == http.MethodPut && status >= 200 && status < 300 {
		if o.published == nil {
			o.published = map[string]struct{}{}
		}
		o.published[tag] = struct{}{}
	}
	if method == http.MethodGet {
		o.reads = append(o.reads, microsandboxManifestRead{tag, status})
	}
}

func (o *microsandboxPrebuildRegistry) publishedRefs(repo string) []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	var refs []string
	for tag := range o.published {
		refs = append(refs, repo+":"+tag)
	}
	return refs
}

func (o *microsandboxPrebuildRegistry) resetReads() { o.mu.Lock(); defer o.mu.Unlock(); o.reads = nil }

func (o *microsandboxPrebuildRegistry) readPublishedHash(ref string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, read := range o.reads {
		if strings.HasSuffix(ref, ":"+read.tag) && read.status == http.StatusOK {
			return true
		}
	}
	return false
}

func (o *microsandboxPrebuildRegistry) missedDifferentHash(ref string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, read := range o.reads {
		if !strings.HasSuffix(ref, ":"+read.tag) && read.status == http.StatusNotFound {
			return true
		}
	}
	return false
}
