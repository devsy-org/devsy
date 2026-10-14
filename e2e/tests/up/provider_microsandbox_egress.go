package up

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

type microsandboxEgressActiveConfig struct {
	Network *microsandboxEgressNetwork `json:"network"`
}

type microsandboxEgressNetwork struct {
	Enabled *bool           `json:"enabled"`
	Policy  json.RawMessage `json:"policy"`
}

type microsandboxEgressEndpoint struct {
	baseURL  string
	nonce    string
	mu       sync.Mutex
	receipts map[string]int
}

var _ = ginkgo.Describe("microsandbox egress parity",
	ginkgo.Label("up-provider-microsandbox-egress"), func() {
		for _, provider := range microsandboxProviders("builtin-egress", "external-egress") {
			ginkgo.Context(provider.name, ginkgo.Label(provider.label), func() {
				var f *framework.Framework
				var workspace, initialDir string
				var endpoint *microsandboxEgressEndpoint

				ginkgo.BeforeEach(func(ctx context.Context) {
					requireMicrosandboxEgressHost()
					skipIfNoMicrosandbox(ctx)
					ginkgo.GinkgoT().Setenv("DEVSY_HOME", ginkgo.GinkgoT().TempDir())
					ginkgo.GinkgoT().Setenv("DEVSY_CONFIG", "")
					var err error
					initialDir, err = os.Getwd()
					framework.ExpectNoError(err)
					f = framework.NewDefaultFramework(filepath.Join(initialDir, "bin"))
					endpoint = newMicrosandboxEgressEndpoint(ctx)
					workspace, err = framework.CreateTempDir()
					framework.ExpectNoError(err)
					ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, workspace)
					framework.ExpectNoError(
						f.DevsyProviderAdd(ctx, provider.source, "--name", provider.name),
					)
					ginkgo.DeferCleanup(cleanupMicrosandboxEgressProvider, f, provider.name)
					ginkgo.DeferCleanup(f.CleanupWorkspace, workspace)
					writeMicrosandboxResourceConfig(workspace, nil)
				})

				ginkgo.DescribeTable(
					"keeps public TCP policy through stop/start and recreation",
					func(ctx context.Context, blocked bool) {
						control := ""
						if blocked {
							var err error
							control, err = framework.CreateTempDir()
							framework.ExpectNoError(err)
							ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, control)
							controlProvider := provider.name + "-control"
							framework.ExpectNoError(
								f.DevsyProviderAdd(ctx, provider.source, "--name", controlProvider),
							)
							ginkgo.DeferCleanup(
								cleanupMicrosandboxEgressProvider,
								f,
								controlProvider,
							)
							ginkgo.DeferCleanup(f.CleanupWorkspace, control)
							writeMicrosandboxResourceConfig(control, nil)
							framework.ExpectNoError(f.DevsyUp(
								ctx,
								control,
								"--provider",
								controlProvider,
								"--devcontainer",
								".devcontainer.json",
							))
							framework.ExpectNoError(f.DevsyProviderUse(ctx, provider.name,
								microsandboxResourceOption, "MICROSANDBOX_BLOCK_EGRESS=true"))
						}
						framework.ExpectNoError(f.DevsyUp(ctx, workspace,
							"--provider", provider.name, "--devcontainer", ".devcontainer.json"))
						sandbox := microsandboxResourceSandbox(ctx, f, workspace)
						createdAt := microsandboxCreationTime(ctx, sandbox)
						bootID := microsandboxStorageBootID(ctx, f, workspace)
						var deniedTokens []string
						assertPolicy := func(phase string) {
							assertMicrosandboxEgressPolicy(ctx, sandbox, blocked)
							endpoint.assertHostReachable(ctx, phase+"-host-before")
							targetToken := phase + "-target"
							if blocked {
								assertMicrosandboxEgressPolicy(
									ctx,
									microsandboxResourceSandbox(ctx, f, control),
									false,
								)
								beforeToken := phase + "-control-before"
								assertMicrosandboxEgressAllowed(
									ctx,
									f,
									control,
									endpoint.url(beforeToken),
								)
								gomega.Expect(endpoint.receiptCount(beforeToken)).
									To(gomega.Equal(1))
								assertMicrosandboxEgressRefused(
									ctx,
									f,
									workspace,
									endpoint.url(targetToken),
								)
								deniedTokens = append(deniedTokens, targetToken)
								afterToken := phase + "-control-after"
								assertMicrosandboxEgressAllowed(
									ctx,
									f,
									control,
									endpoint.url(afterToken),
								)
								gomega.Expect(endpoint.receiptCount(afterToken)).To(gomega.Equal(1))
							} else {
								assertMicrosandboxEgressAllowed(
									ctx,
									f,
									workspace,
									endpoint.url(targetToken),
								)
								gomega.Expect(endpoint.receiptCount(targetToken)).
									To(gomega.Equal(1))
							}
							endpoint.assertHostReachable(ctx, phase+"-host-after")
							for _, token := range deniedTokens {
								gomega.Expect(endpoint.receiptCount(token)).
									To(gomega.BeZero(), token)
							}
						}
						assertPolicy("created")
						framework.ExpectNoError(f.DevsyWorkspaceStop(ctx, workspace))
						framework.ExpectNoError(f.DevsyUp(ctx, workspace))
						gomega.Expect(microsandboxCreationTime(ctx, sandbox)).
							To(gomega.Equal(createdAt))
						restartedBootID := microsandboxStorageBootID(ctx, f, workspace)
						gomega.Expect(restartedBootID).NotTo(gomega.Equal(bootID))
						assertPolicy("restarted")
						framework.ExpectNoError(f.DevsyUpRecreate(ctx, workspace))
						gomega.Expect(microsandboxCreationTime(ctx, sandbox)).
							NotTo(gomega.Equal(createdAt))
						gomega.Expect(microsandboxStorageBootID(ctx, f, workspace)).
							NotTo(gomega.Equal(restartedBootID))
						assertPolicy("recreated")
					},
					ginkgo.Entry("allows the controlled public destination by default", false,
						ginkgo.SpecTimeout(framework.TimeoutLong())),
					ginkgo.Entry(
						"denies the same reachable destination when block egress is enabled",
						true,
						ginkgo.SpecTimeout(framework.TimeoutLong()),
					),
				)
			})
		}
	})

func assertMicrosandboxEgressPolicy(ctx context.Context, sandbox string, blocked bool) {
	out, err := microsandboxResourceCommand(
		ctx,
		"inspect",
		sandbox,
		"--format",
		microsandboxResourceJSON,
	)
	framework.ExpectNoError(err, string(out))
	var inspection struct {
		ActiveConfig *microsandboxEgressActiveConfig `json:"active_config"`
	}
	framework.ExpectNoError(json.Unmarshal(out, &inspection), string(out))
	gomega.Expect(inspection.ActiveConfig).NotTo(gomega.BeNil())
	network := inspection.ActiveConfig.Network
	gomega.Expect(network).NotTo(gomega.BeNil())
	gomega.Expect(network.Enabled).NotTo(gomega.BeNil())
	gomega.Expect(*network.Enabled).To(gomega.BeTrue())
	if blocked {
		gomega.Expect(string(network.Policy)).To(gomega.MatchJSON(`{
			"default_egress": "deny", "default_ingress": "allow", "rules": []
		}`))
		return
	}
	if len(network.Policy) == 0 {
		// v0.7.7 omits the default policy from SandboxConfig; its engine resolves Public.
		// The paired guest fetch must still prove that this active configuration permits egress.
		return
	}
	gomega.Expect(string(network.Policy)).To(gomega.MatchJSON(`{
		"default_egress": "deny", "default_ingress": "allow",
		"rules": [
			{"direction": "egress", "destination": {"group": "host"},
			 "protocols": ["udp", "tcp"], "ports": [{"start": 53, "end": 53}], "action": "allow"},
			{"direction": "egress", "destination": {"group": "public"},
			 "protocols": [], "ports": [], "action": "allow"}
		]
	}`))
}

func requireMicrosandboxEgressHost() {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		message := "MicroSandbox controlled egress fixture requires Linux with root/CAP_NET_ADMIN"
		if os.Getenv("DEVSY_REQUIRE_MICROSANDBOX") == "true" {
			ginkgo.Fail(message)
		}
		ginkgo.Skip(message)
	}
	_, err := exec.LookPath("ip")
	framework.ExpectNoError(err, "iproute2 is required for the test-owned egress interface")
}

func newMicrosandboxEgressEndpoint(ctx context.Context) *microsandboxEgressEndpoint {
	var random [8]byte
	_, err := rand.Read(random[:])
	framework.ExpectNoError(err)
	nonce := hex.EncodeToString(random[:])
	name := "dse" + nonce[:10]
	address := fmt.Sprintf("198.19.%d.%d", random[0], random[1])
	createMicrosandboxEgressInterface(ctx, name, address)
	listener, err := net.Listen("tcp4", net.JoinHostPort(address, "0"))
	framework.ExpectNoError(err)
	endpoint := &microsandboxEgressEndpoint{
		baseURL:  "http://" + listener.Addr().String() + "/" + nonce + "/",
		nonce:    nonce,
		receipts: microsandboxEgressProbeReceipts(),
	}
	server := &http.Server{
		Handler:           endpoint,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       5 * time.Second,
	}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	ginkgo.DeferCleanup(func(cleanupCtx context.Context) error {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(cleanupCtx), 10*time.Second)
		defer cancel()
		closeErr := server.Close()
		select {
		case serveErr := <-finished:
			if errors.Is(serveErr, http.ErrServerClosed) {
				return closeErr
			}
			return errors.Join(closeErr, serveErr)
		case <-cleanupCtx.Done():
			return errors.Join(closeErr, cleanupCtx.Err())
		}
	})
	// Port 80 can return a synthetic denial response without contacting the endpoint.
	tcpAddress, ok := listener.Addr().(*net.TCPAddr)
	gomega.Expect(ok).To(gomega.BeTrue())
	gomega.Expect(tcpAddress.Port).To(gomega.BeNumerically(">", 1024))
	endpoint.assertHostReachable(ctx, "fixture-ready")
	return endpoint
}

func createMicrosandboxEgressInterface(ctx context.Context, name, address string) {
	interfaces, err := net.Interfaces()
	framework.ExpectNoError(err)
	for _, existing := range interfaces {
		gomega.Expect(existing.Name).
			NotTo(gomega.Equal(name), "refuse to reuse an existing interface")
	}
	addresses, err := net.InterfaceAddrs()
	framework.ExpectNoError(err)
	for _, existing := range addresses {
		existingAddress, _, _ := strings.Cut(existing.String(), "/")
		gomega.Expect(existingAddress).
			NotTo(gomega.Equal(address), "refuse to reuse an existing address")
	}
	// v0.7.7 classifies this benchmark range as Public and dials non-gateway IPs directly.
	framework.ExpectNoError(microsandboxEgressIP(ctx, "link", "add", "name", name, "type", "dummy"))
	ginkgo.DeferCleanup(func(cleanupCtx context.Context) error {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(cleanupCtx), time.Minute)
		defer cancel()
		return microsandboxEgressIP(cleanupCtx, "link", "delete", "dev", name)
	})
	framework.ExpectNoError(microsandboxEgressIP(ctx, "address", "add", address+"/32", "dev", name))
	framework.ExpectNoError(microsandboxEgressIP(ctx, "link", "set", "dev", name, "up"))
}

func microsandboxEgressIP(ctx context.Context, args ...string) error {
	commandCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// #nosec G204 -- fixed ip executable and generated test-owned interface/address arguments.
	out, err := exec.CommandContext(commandCtx, "ip", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip %v: %w: %s", args, err, out)
	}
	return nil
}

func microsandboxEgressProbeReceipts() map[string]int {
	receipts := map[string]int{"fixture-ready": 0}
	for _, phase := range []string{"created", "restarted", "recreated"} {
		for _, probe := range []string{"host-before", "host-after", "target", "control-before", "control-after"} {
			receipts[phase+"-"+probe] = 0
		}
	}
	return receipts
}

func (endpoint *microsandboxEgressEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	endpoint.mu.Lock()
	// Only server-owned probe tokens can be counted or included in the response.
	for token := range endpoint.receipts {
		if r.URL.Path != "/"+endpoint.nonce+"/"+token {
			continue
		}
		endpoint.receipts[token]++
		endpoint.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain")
		if _, err := io.WriteString(w, endpoint.nonce+":"+token+"\n"); err != nil {
			return
		}
		return
	}
	endpoint.mu.Unlock()
	http.NotFound(w, r)
}

func (endpoint *microsandboxEgressEndpoint) url(token string) string {
	return endpoint.baseURL + token
}

func (endpoint *microsandboxEgressEndpoint) receiptCount(token string) int {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	return endpoint.receipts[token]
}

func (endpoint *microsandboxEgressEndpoint) assertHostReachable(ctx context.Context, token string) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.url(token), nil)
	framework.ExpectNoError(err)
	client := &http.Client{
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
		Timeout:   5 * time.Second,
	}
	response, err := client.Do(request)
	framework.ExpectNoError(err)
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1024))
	closeErr := response.Body.Close()
	framework.ExpectNoError(readErr)
	framework.ExpectNoError(closeErr)
	gomega.Expect(response.StatusCode).To(gomega.Equal(http.StatusOK))
	gomega.Expect(string(body)).To(gomega.Equal(endpoint.nonce + ":" + token + "\n"))
	gomega.Expect(endpoint.receiptCount(token)).To(gomega.Equal(1))
}

func assertMicrosandboxEgressAllowed(
	ctx context.Context,
	f *framework.Framework,
	workspace, url string,
) {
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := f.DevsySSHOnce(probeCtx, workspace,
		microsandboxEgressClientCheck()+microsandboxEgressFetch(url)+" 2>/dev/null")
	framework.ExpectNoError(err, out)
	parts := strings.Split(url, "/")
	gomega.Expect(len(parts)).To(gomega.BeNumerically(">=", 2))
	gomega.Expect(out).To(gomega.Equal(parts[len(parts)-2] + ":" + parts[len(parts)-1] + "\n"))
}

func assertMicrosandboxEgressRefused(
	ctx context.Context,
	f *framework.Framework,
	workspace, url string,
) {
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	command := microsandboxEgressClientCheck() +
		"if diagnostic=$(" + microsandboxEgressFetch(url) + " 2>&1); then status=0; else status=$?; fi; " +
		"if [ \"$status\" -ne 1 ]; then " +
		"printf 'unexpected wget status %s: %s\\n' \"$status\" \"$diagnostic\"; exit 92; fi; " +
		"case \"$diagnostic\" in " +
		"*'Connection refused'*|*'Connection reset by peer'*) printf 'egress-refused\\n';; " +
		"*) printf 'unexpected network failure: %s\\n' \"$diagnostic\"; exit 93;; esac"
	out, err := f.DevsySSHOnce(probeCtx, workspace, command)
	framework.ExpectNoError(err, out)
	gomega.Expect(out).To(gomega.Equal("egress-refused\n"))
}

func microsandboxEgressClientCheck() string {
	return "command -v busybox >/dev/null || exit 90; busybox wget --help >/dev/null 2>&1 || exit 91; "
}

func microsandboxEgressFetch(url string) string {
	return "LC_ALL=C http_proxy= https_proxy= HTTP_PROXY= HTTPS_PROXY= ALL_PROXY= all_proxy= " +
		"busybox wget -q -T 5 -O - '" + url + "'"
}

func cleanupMicrosandboxEgressProvider(
	ctx context.Context,
	f *framework.Framework,
	provider string,
) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	return f.DevsyProviderDelete(cleanupCtx, provider)
}
