//go:build linux

package up

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/e2e/framework/registryfixture"
	devsyconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/driver/external"
	"github.com/devsy-org/devsy/pkg/image"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

type microsandboxConsumerAttempt struct {
	name          string
	credentials   bool
	trustFixture  bool
	wantErrorText string
}

type microsandboxConsumerSeed struct {
	handler        http.Handler
	repository     string
	imageDigest    string
	markerPath     string
	markerContent  string
	expectedBodies map[string]int64
}

type microsandboxConsumerHelper struct {
	root, config, log, operationID string
}

type microsandboxConsumerEnvironment struct {
	fixture       *registryfixture.Server
	home, msbHome string
	helper        microsandboxConsumerHelper
	trustFixture  bool
}

type microsandboxConsumerReceiptExpectation struct {
	path, operationID, serverKey, providerBinary, providerHash string
	required                                                   bool
	outcome                                                    string
}

type microsandboxConsumerProvider struct {
	config        provider.ProviderAgentConfig
	origin        string
	runtimeBinary string
	runtimeHash   string
}

type microsandboxConsumerHostRun struct {
	host          *external.Host
	workspaceID   string
	imageRef      string
	attempt       microsandboxConsumerAttempt
	markerPath    string
	markerContent string
}

const (
	microsandboxConsumerPositive      = "positive"
	microsandboxConsumerNoCA          = "missing-ca"
	microsandboxConsumerNoCredentials = "missing-credentials"
)

var _ = ginkgo.Describe(
	"published MicroSandbox provider registry consumption",
	ginkgo.Label("up-provider-microsandbox-registry-consumer", "microsandbox-external"),
	func() {
		ginkgo.DescribeTable(
			"uses the published provider's registry client",
			runMicrosandboxConsumerAttempt,
			ginkgo.Entry(
				"downloads an authenticated image over fixture TLS",
				microsandboxConsumerAttempt{
					name: microsandboxConsumerPositive, credentials: true, trustFixture: true,
				},
				ginkgo.SpecTimeout(framework.TimeoutLong()),
			),
			ginkgo.Entry("rejects an untrusted registry certificate", microsandboxConsumerAttempt{
				name: microsandboxConsumerNoCA, credentials: true, wantErrorText: "certificate",
			}, ginkgo.SpecTimeout(framework.TimeoutLong())),
			ginkgo.Entry("rejects a registry without credentials", microsandboxConsumerAttempt{
				name:          microsandboxConsumerNoCredentials,
				trustFixture:  true,
				wantErrorText: "unauthorized",
			}, ginkgo.SpecTimeout(framework.TimeoutLong())),
		)
	},
)

func runMicrosandboxConsumerAttempt(ctx context.Context, attempt microsandboxConsumerAttempt) {
	session := newMicrosandboxTestSession(ctx)
	providerName := "case-a-consumer"
	session.addProvider(ctx, microsandboxExternalProvider, providerName)
	consumerProvider := prepareMicrosandboxConsumerProvider(ctx, providerName)
	actualProviderHash := consumerProvider.runtimeHash
	runtimeBinary := consumerProvider.runtimeBinary

	seed := seedMicrosandboxConsumerImage(ctx, attempt.name)

	fixture := newMicrosandboxConsumerRegistry(seed.handler)
	imageRef := fixture.Authority + "/" + seed.repository + "@" + seed.imageDigest
	assertMicrosandboxImageAbsentFromDocker(ctx, imageRef)

	workspaceID := microsandboxConsumerID(attempt.name)
	privateHome := microsandboxConsumerDirectory(ginkgo.GinkgoT().TempDir())
	msbHome := microsandboxConsumerDirectory(ginkgo.GinkgoT().TempDir())
	cacheDir := filepath.Join(msbHome, "cache")
	_, err := os.Stat(cacheDir)
	gomega.Expect(err).
		To(gomega.MatchError(os.ErrNotExist), "fresh MSB_HOME cache must not contain a prior image")
	helper := prepareMicrosandboxConsumerHelper(
		fixture, privateHome, actualProviderHash, attempt.credentials,
	)
	setMicrosandboxConsumerEnvironment(microsandboxConsumerEnvironment{
		fixture: fixture, home: privateHome, msbHome: msbHome,
		helper: helper, trustFixture: attempt.trustFixture,
	})

	host := newMicrosandboxConsumerHost(ctx, providerName, workspaceID, consumerProvider)
	ginkgo.DeferCleanup(func(cleanupCtx ginkgo.SpecContext) error {
		deleteCtx, cancel := context.WithTimeout(context.WithoutCancel(cleanupCtx), time.Minute)
		defer cancel()
		return host.DeleteDevContainer(deleteCtx, workspaceID)
	})
	runMicrosandboxConsumerHost(ctx, microsandboxConsumerHostRun{
		host: host, workspaceID: workspaceID, imageRef: imageRef, attempt: attempt,
		markerPath: seed.markerPath, markerContent: seed.markerContent,
	})

	framework.ExpectNoError(closeMicrosandboxConsumerRegistry(ctx, fixture))
	snapshot := fixture.Snapshot()
	assertMicrosandboxConsumerSnapshot(attempt, snapshot, seed.expectedBodies)
	receiptRequired := attempt.name == microsandboxConsumerPositive ||
		attempt.name == microsandboxConsumerNoCredentials
	expectedOutcome := ""
	switch attempt.name {
	case microsandboxConsumerPositive:
		expectedOutcome = "credentials_provided"
	case microsandboxConsumerNoCredentials:
		expectedOutcome = "credentials_not_found"
	}
	assertMicrosandboxConsumerReceipts(microsandboxConsumerReceiptExpectation{
		path: helper.log, operationID: helper.operationID, serverKey: fixture.Authority,
		providerBinary: runtimeBinary, providerHash: actualProviderHash,
		required: receiptRequired, outcome: expectedOutcome,
	})
}

func newMicrosandboxConsumerRegistry(handler http.Handler) *registryfixture.Server {
	fixture, err := registryfixture.Start(handler, registryfixture.Config{
		Username: "case-a-fixture-user", Password: "case-a-private-fixture-secret",
	})
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(func(ctx ginkgo.SpecContext) error {
		return closeMicrosandboxConsumerRegistry(ctx, fixture)
	})
	return fixture
}

func closeMicrosandboxConsumerRegistry(ctx context.Context, fixture *registryfixture.Server) error {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return fixture.Close(closeCtx)
}

func prepareMicrosandboxConsumerProvider(
	ctx context.Context,
	name string,
) microsandboxConsumerProvider {
	installed, err := provider.LoadProviderConfig(devsyconfig.DefaultContext, name)
	framework.ExpectNoError(err)
	origin := ginkgo.GinkgoT().TempDir()
	binaryDir := filepath.Join(origin, "binaries")
	framework.ExpectNoError(os.MkdirAll(binaryDir, 0o700))
	binaryPaths, err := provider.DownloadBinaries(ctx, installed.Agent.Binaries, binaryDir)
	framework.ExpectNoError(err)
	binary, expectedHash := microsandboxConsumerBinary(installed.Agent, binaryPaths)
	actualHash := microsandboxConsumerFileSHA256(binary)
	gomega.Expect(actualHash).To(gomega.Equal(expectedHash))
	return microsandboxConsumerProvider{
		config: installed.Agent, origin: origin, runtimeBinary: binary, runtimeHash: actualHash,
	}
}

func newMicrosandboxConsumerHost(
	ctx context.Context,
	name, workspaceID string,
	consumerProvider microsandboxConsumerProvider,
) *external.Host {
	devsyConfig, err := devsyconfig.LoadConfig("", "")
	framework.ExpectNoError(err)
	workspace := &provider.Workspace{
		ID: workspaceID, UID: workspaceID, Context: devsyconfig.DefaultContext,
		Provider: provider.WorkspaceProviderConfig{Name: name}, Origin: consumerProvider.origin,
	}
	info := &provider.AgentWorkspaceInfo{
		WorkspaceOrigin: consumerProvider.origin,
		Workspace:       workspace,
		Agent:           consumerProvider.config,
		Options: provider.GetProviderOptions(
			workspace,
			nil,
			devsyConfig,
		),
		Origin: consumerProvider.origin,
	}
	host, err := external.New(ctx, info)
	framework.ExpectNoError(err)
	return host
}

func runMicrosandboxConsumerHost(ctx context.Context, run microsandboxConsumerHostRun) {
	framework.ExpectNoError(run.host.ProvisioningPreflight(ctx))
	found, err := run.host.FindDevContainer(ctx, run.workspaceID)
	framework.ExpectNoError(err)
	gomega.Expect(found).To(gomega.BeNil(), "workspace ID must be fresh before RunImage")
	err = run.host.RunImage(ctx, &runtimev1.RunImageRequest{
		WorkspaceId: run.workspaceID, Image: run.imageRef,
		User: microsandboxRootUser, RemoteUser: microsandboxRootUser,
		Entrypoint: "/bin/sh", Args: []string{"-c", "sleep 600"},
	})
	if run.attempt.wantErrorText != "" {
		gomega.Expect(err).To(gomega.HaveOccurred())
		gomega.Expect(strings.ToLower(err.Error())).
			To(gomega.ContainSubstring(run.attempt.wantErrorText))
		found, err = run.host.FindDevContainer(ctx, run.workspaceID)
		framework.ExpectNoError(err)
		gomega.Expect(found).To(gomega.BeNil(), "failed RunImage must not leave a workspace")
		return
	}
	framework.ExpectNoError(err)
	var stdout strings.Builder
	framework.ExpectNoError(run.host.CommandDevContainer(ctx, &driver.CommandParams{
		WorkspaceID: run.workspaceID, Command: "cat " + run.markerPath, Stdout: &stdout,
	}))
	gomega.Expect(stdout.String()).To(gomega.Equal(run.markerContent))
	found, err = run.host.FindDevContainer(ctx, run.workspaceID)
	framework.ExpectNoError(err)
	gomega.Expect(found).NotTo(gomega.BeNil())
}

func seedMicrosandboxConsumerImage(ctx context.Context, attempt string) microsandboxConsumerSeed {
	img, err := image.GetImageForArch(
		ctx,
		"ghcr.io/devsy-org/test-images/base:alpine",
		runtime.GOARCH,
	)
	framework.ExpectNoError(err)
	img, markerPath, markerContent := appendMicrosandboxConsumerMarker(img, attempt)
	imageDigest, err := img.Digest()
	framework.ExpectNoError(err)

	seedHandler := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	seed := httptest.NewServer(seedHandler)
	seedClosed := false
	ginkgo.DeferCleanup(func() {
		if !seedClosed {
			seed.Close()
		}
	})
	operationID := microsandboxConsumerID(attempt)
	repository := "case-a/" + strings.TrimPrefix(operationID, "case-a-")
	seedRef, err := name.NewTag(
		strings.TrimPrefix(seed.URL, "http://")+"/"+repository+":fixture",
		name.Insecure,
	)
	framework.ExpectNoError(err)
	framework.ExpectNoError(remote.Write(seedRef, img, remote.WithContext(ctx)))
	seed.Close()
	seedClosed = true
	expected := microsandboxConsumerImageBodies(img)
	return microsandboxConsumerSeed{
		handler: seedHandler, repository: repository, imageDigest: imageDigest.String(),
		markerPath: markerPath, markerContent: markerContent, expectedBodies: expected,
	}
}

func appendMicrosandboxConsumerMarker(img v1.Image, attempt string) (v1.Image, string, string) {
	markerToken := microsandboxConsumerID(attempt)
	markerPath := "/case-a-marker-" + strings.TrimPrefix(markerToken, "case-a-")
	markerContent := "published-provider-image-" + markerToken + "\n"
	var markerTar bytes.Buffer
	tarWriter := tar.NewWriter(&markerTar)
	framework.ExpectNoError(tarWriter.WriteHeader(&tar.Header{
		Name: strings.TrimPrefix(markerPath, "/"), Mode: 0o644,
		Size: int64(len(markerContent)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR,
	}))
	_, err := io.WriteString(tarWriter, markerContent)
	framework.ExpectNoError(err)
	framework.ExpectNoError(tarWriter.Close())
	markerLayer, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(markerTar.Bytes())), nil
	})
	framework.ExpectNoError(err)
	imageWithMarker, err := mutate.AppendLayers(img, markerLayer)
	framework.ExpectNoError(err)
	return imageWithMarker, markerPath, markerContent
}

func microsandboxConsumerImageBodies(img v1.Image) map[string]int64 {
	manifestBytes, err := img.RawManifest()
	framework.ExpectNoError(err)
	manifestHash := sha256.Sum256(manifestBytes)
	expected := map[string]int64{
		"sha256:" + hex.EncodeToString(manifestHash[:]): int64(len(manifestBytes)),
	}
	configBytes, err := img.RawConfigFile()
	framework.ExpectNoError(err)
	configDigest, err := img.ConfigName()
	framework.ExpectNoError(err)
	expected[configDigest.String()] = int64(len(configBytes))
	layers, err := img.Layers()
	framework.ExpectNoError(err)
	for _, layer := range layers {
		digest, digestErr := layer.Digest()
		framework.ExpectNoError(digestErr)
		size, sizeErr := layer.Size()
		framework.ExpectNoError(sizeErr)
		expected[digest.String()] = size
	}
	return expected
}

func microsandboxConsumerBinary(
	agent provider.ProviderAgentConfig,
	binaryPaths map[string]string,
) (string, string) {
	binaryName := agent.External.Binary
	binaryPath := binaryPaths[binaryName]
	gomega.Expect(binaryPath).
		NotTo(gomega.BeEmpty(), "published provider runtime binary was not downloaded")
	var expected string
	for _, binary := range agent.Binaries[binaryName] {
		if binary.OS == runtime.GOOS && binary.Arch == runtime.GOARCH {
			expected = strings.TrimPrefix(strings.ToLower(binary.Checksum), "sha256:")
			break
		}
	}
	gomega.Expect(expected).
		To(gomega.HaveLen(sha256.Size*2), "published runtime checksum must be pinned")
	return binaryPath, expected
}

func microsandboxConsumerFileSHA256(path string) string {
	// #nosec G304 -- DownloadBinaries returns this path inside the spec's private temporary origin.
	file, err := os.Open(path)
	framework.ExpectNoError(err)
	defer func() { framework.ExpectNoError(file.Close()) }()
	h := sha256.New()
	_, err = io.Copy(h, file)
	framework.ExpectNoError(err)
	return hex.EncodeToString(h.Sum(nil))
}

func microsandboxConsumerID(suffix string) string {
	var token [8]byte
	_, err := rand.Read(token[:])
	framework.ExpectNoError(err)
	return "case-a-" + suffix + "-" + hex.EncodeToString(token[:])
}

func microsandboxConsumerDirectory(path string) string {
	// #nosec G302 -- Ginkgo owns this fresh test directory; private 0700 access is required.
	framework.ExpectNoError(os.Chmod(path, 0o700))
	return path
}

func prepareMicrosandboxConsumerHelper(
	fixture *registryfixture.Server,
	home, providerHash string,
	credentials bool,
) microsandboxConsumerHelper {
	root, err := os.MkdirTemp(home, "helper-")
	framework.ExpectNoError(err)
	root, err = filepath.EvalSymlinks(root)
	framework.ExpectNoError(err)
	// #nosec G302 -- the helper's executable/config/log tree is deliberately private to this spec.
	framework.ExpectNoError(os.Chmod(root, 0o700))
	binDir := filepath.Join(root, "bin")
	framework.ExpectNoError(os.Mkdir(binDir, 0o700))
	executable, err := os.Executable()
	framework.ExpectNoError(err)
	framework.ExpectNoError(
		os.Symlink(executable, filepath.Join(binDir, registryfixture.HelperBinaryName)),
	)
	operationID := microsandboxConsumerID("operation")
	configPath, logPath := filepath.Join(
		root,
		"credentials.json",
	), filepath.Join(
		root,
		"observations.jsonl",
	)
	config := registryfixture.CredentialConfig{
		OperationID: operationID, ServerKey: fixture.Authority,
		CredentialsAvailable: credentials, ExpectedProviderSHA256: providerHash,
	}
	if credentials {
		config.Username, config.Secret = fixture.Username, fixture.Password
	}
	// #nosec G117 -- private helper config intentionally holds fixture credentials and is written mode 0600.
	data, err := json.Marshal(config)
	framework.ExpectNoError(err)
	framework.ExpectNoError(os.WriteFile(configPath, data, 0o600))
	dockerDir := filepath.Join(home, ".docker")
	framework.ExpectNoError(os.Mkdir(dockerDir, 0o700))
	framework.ExpectNoError(os.Mkdir(filepath.Join(home, ".config"), 0o700))
	dockerConfig, err := json.Marshal(struct {
		CredHelpers map[string]string `json:"credHelpers"`
	}{CredHelpers: map[string]string{fixture.Authority: "fixture"}})
	framework.ExpectNoError(err)
	framework.ExpectNoError(
		os.WriteFile(filepath.Join(dockerDir, "config.json"), dockerConfig, 0o600),
	)
	return microsandboxConsumerHelper{
		root:        root,
		config:      configPath,
		log:         logPath,
		operationID: operationID,
	}
}

func setMicrosandboxConsumerEnvironment(environment microsandboxConsumerEnvironment) {
	fixture, home, msbHome := environment.fixture, environment.home, environment.msbHome
	helper := environment.helper
	binDir := filepath.Join(helper.root, "bin")
	for name, value := range map[string]string{
		"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"DOCKER_CONFIG": filepath.Join(home, ".docker"), "MSB_HOME": msbHome,
		"MSB_CONFIG_PATH":        filepath.Join(msbHome, "config.json"),
		"CASE_A_CREDENTIAL_ROOT": helper.root, "CASE_A_CREDENTIAL_CONFIG": helper.config,
		"CASE_A_CREDENTIAL_OBSERVATIONS": helper.log, "CASE_A_OPERATION_ID": helper.operationID,
		"HTTPS_PROXY": fixture.ProxyURL, "HTTP_PROXY": fixture.ProxyURL,
		"https_proxy": fixture.ProxyURL, "http_proxy": fixture.ProxyURL,
		"NO_PROXY": "", "no_proxy": "", "REGISTRY_AUTH_FILE": "",
		"PATH": binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	} {
		ginkgo.GinkgoT().Setenv(name, value)
	}
	certPath := filepath.Join(home, "fixture-ca.pem")
	cert := []byte(nil)
	if environment.trustFixture {
		cert = fixture.CAPEM
	}
	framework.ExpectNoError(os.WriteFile(certPath, cert, 0o600))
	framework.ExpectNoError(os.Mkdir(filepath.Join(home, "empty-cert-dir"), 0o700))
	ginkgo.GinkgoT().Setenv("SSL_CERT_FILE", certPath)
	ginkgo.GinkgoT().Setenv("SSL_CERT_DIR", filepath.Join(home, "empty-cert-dir"))
}

func assertMicrosandboxConsumerSnapshot(
	attempt microsandboxConsumerAttempt,
	snapshot registryfixture.Snapshot,
	expected map[string]int64,
) {
	gomega.Expect(snapshot.ConnectAllowed).To(gomega.BeNumerically(">", 0))
	gomega.Expect(snapshot.ConnectDenied).To(gomega.BeZero())
	switch attempt.name {
	case microsandboxConsumerPositive:
		gomega.Expect(snapshot.Authenticated).To(gomega.BeNumerically(">", 0))
		gomega.Expect(snapshot.ManifestGets).To(gomega.BeNumerically(">", 0))
		gomega.Expect(snapshot.BlobGets).To(gomega.BeNumerically(">", 0))
		assertMicrosandboxConsumerPositiveRequests(snapshot.Requests, expected)
	case microsandboxConsumerNoCA:
		gomega.Expect(snapshot.Authenticated).To(gomega.BeZero())
		gomega.Expect(snapshot.AuthRejected).To(gomega.BeZero())
		gomega.Expect(snapshot.Requests).
			To(gomega.BeEmpty(), "TLS rejection must happen before HTTP requests")
	case microsandboxConsumerNoCredentials:
		gomega.Expect(snapshot.Authenticated).To(gomega.BeZero())
		gomega.Expect(snapshot.AuthRejected).To(gomega.BeNumerically(">", 0))
		gomega.Expect(snapshot.ManifestGets).To(gomega.BeZero())
		gomega.Expect(snapshot.BlobGets).To(gomega.BeZero())
		assertMicrosandboxConsumerChallenges(snapshot.Requests)
	default:
		ginkgo.Fail(fmt.Sprintf("unknown consumer attempt %q", attempt.name))
	}
}

func assertMicrosandboxConsumerPositiveRequests(
	requests []registryfixture.Request,
	expected map[string]int64,
) {
	observed := map[string]int64{}
	for _, request := range requests {
		if !request.Authenticated {
			assertMicrosandboxConsumerChallenge(request)
			continue
		}
		gomega.Expect(request.Status).To(gomega.BeNumerically(">=", 200))
		gomega.Expect(request.Status).To(gomega.BeNumerically("<", 300))
		if request.Method != http.MethodGet ||
			(request.Path != "manifests" && request.Path != "blobs") {
			continue
		}
		gomega.Expect(request.BodyDigest).NotTo(gomega.BeEmpty())
		expectedSize, knownDigest := expected[request.BodyDigest]
		gomega.Expect(knownDigest).
			To(gomega.BeTrue(), "provider fetched an unexpected image body %s", request.BodyDigest)
		gomega.Expect(request.Bytes).To(gomega.Equal(expectedSize))
		observed[request.BodyDigest] = request.Bytes
	}
	for digest, size := range expected {
		gomega.Expect(observed[digest]).
			To(gomega.Equal(size), "published image component %s was not received byte-for-byte", digest)
	}
}

func assertMicrosandboxConsumerChallenges(requests []registryfixture.Request) {
	for _, request := range requests {
		gomega.Expect(request.Authenticated).To(gomega.BeFalse())
		gomega.Expect(request.Method).To(gomega.Equal(http.MethodGet))
		// An anonymous client can receive the Basic challenge, then be refused
		// again when it requests the protected manifest without credentials.
		gomega.Expect(request.Path).To(gomega.Or(gomega.Equal("v2"), gomega.Equal("manifests")))
		gomega.Expect(request.Status).To(gomega.Equal(http.StatusUnauthorized))
	}
}

func assertMicrosandboxConsumerChallenge(request registryfixture.Request) {
	gomega.Expect(request.Method).To(gomega.Equal(http.MethodGet))
	gomega.Expect(request.Path).To(gomega.Equal("v2"))
	gomega.Expect(request.Status).To(gomega.Equal(http.StatusUnauthorized))
}

func assertMicrosandboxConsumerReceipts(expected microsandboxConsumerReceiptExpectation) {
	data, err := os.ReadFile(expected.path)
	if os.IsNotExist(err) && !expected.required {
		return
	}
	framework.ExpectNoError(err)
	if len(data) == 0 {
		gomega.Expect(expected.required).
			To(gomega.BeFalse(), "provider did not invoke its credential helper")
		return
	}
	if strings.Contains(string(data), "case-a-private-fixture-secret") {
		ginkgo.Fail("credential observations included secret material")
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var observation registryfixture.CredentialObservation
		framework.ExpectNoError(json.Unmarshal([]byte(line), &observation))
		gomega.Expect(observation.OperationID).To(gomega.Equal(expected.operationID))
		gomega.Expect(observation.ServerKey).To(gomega.Equal(expected.serverKey))
		gomega.Expect(observation.Action).To(gomega.Equal("get"))
		gomega.Expect(observation.Parent.Executable).To(gomega.Equal(expected.providerBinary))
		gomega.Expect(observation.Parent.SHA256).To(gomega.Equal(expected.providerHash))
		gomega.Expect(observation.Parent.StartTimeTicks).To(gomega.BeNumerically(">", 0))
		if expected.outcome != "" {
			gomega.Expect(observation.Outcome).To(gomega.Equal(expected.outcome))
		}
	}
}
