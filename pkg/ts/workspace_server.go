package ts

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	sshServer "github.com/devsy-org/devsy/pkg/ssh/server"
	"tailscale.com/client/local"
	"tailscale.com/envknob"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/ipn/store/mem"
	"tailscale.com/tsnet"
	"tailscale.com/types/key"
)

const (
	// TSPortForwardPort is the fixed port on which the workspace WebSocket reverse proxy listens.
	TSPortForwardPort string = "12051"

	// DevsyTSNetDomain is the MagicDNS suffix for the Devsy tailnet.
	DevsyTSNetDomain = "ts.devsy"

	RunnerProxySocket string = "runner-proxy.sock"

	netMapCooldown = 30 * time.Second
)

// WorkspaceServer holds the TSNet server and its listeners.
type WorkspaceServer struct {
	tsServer  *tsnet.Server
	listeners []net.Listener

	connectionCount atomic.Int64

	config *WorkspaceServerConfig
}

// WorkspaceServerConfig defines configuration for the TSNet instance.
type WorkspaceServerConfig struct {
	AccessKey     string
	PlatformHost  string
	WorkspaceHost string
	LogF          func(format string, args ...any)
	RootDir       string
	// Insecure skips TLS certificate verification for the DERP probe and
	// the TSNet control-plane connection. Only set for coordinators known
	// to use self-signed certificates.
	Insecure bool
}

// NewWorkspaceServer creates a new TSNet server instance.
func NewWorkspaceServer(config *WorkspaceServerConfig) *WorkspaceServer {
	return &WorkspaceServer{
		config: config,
	}
}

// Start initializes the TSNet server, sets up listeners for SSH and HTTP
// reverse proxy traffic, and waits until the given context is canceled.
func (s *WorkspaceServer) Start(ctx context.Context) error {
	log.Infof("starting workspace server")

	workspaceName, projectName, err := s.setupTSNet(ctx)
	if err != nil {
		return err
	}
	lc, err := s.tsServer.LocalClient()
	if err != nil {
		return err
	}

	runner := &runnerClient{
		lc:            lc,
		accessKey:     s.config.AccessKey,
		projectName:   projectName,
		workspaceName: workspaceName,
	}

	go s.sendHeartbeats(ctx, runner)
	go s.watchNetmap(ctx, lc)

	if err := s.startListeners(ctx, runner); err != nil {
		return err
	}

	<-ctx.Done()
	return nil
}

// Stop shuts down all listeners and the TSNet server.
func (s *WorkspaceServer) Stop() {
	for _, listener := range s.listeners {
		if listener != nil {
			_ = listener.Close()
		}
	}
	if s.tsServer != nil {
		_ = s.tsServer.Close()
		s.tsServer = nil
	}
	log.Info("Tailscale server stopped")
}

// Dial dials the given address using the TSNet server.
func (s *WorkspaceServer) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	if s.tsServer == nil {
		return nil, fmt.Errorf("tailscale server is not running")
	}
	return s.tsServer.Dial(ctx, network, addr)
}

// setupTSNet validates configuration, sets up the control URL, starts the TSNet server,
// and parses the hostname into workspace and project names.
func (s *WorkspaceServer) setupTSNet(ctx context.Context) (workspace, project string, err error) {
	if err = s.validateConfig(); err != nil {
		return "", "", err
	}

	baseURL, err := s.setupControlURL(ctx)
	if err != nil {
		return "", "", err
	}

	if err = s.initTsServer(ctx, baseURL); err != nil {
		return "", "", err
	}

	return s.parseWorkspaceHostname()
}

// validateConfig ensures required configuration values are set.
func (s *WorkspaceServer) validateConfig() error {
	if s.config.AccessKey == "" || s.config.PlatformHost == "" || s.config.WorkspaceHost == "" {
		return fmt.Errorf("access key, host, or hostname cannot be empty")
	}
	return nil
}

// setupControlURL constructs the control URL and verifies DERP connection.
func (s *WorkspaceServer) setupControlURL(ctx context.Context) (*url.URL, error) {
	baseURL := &url.URL{
		Scheme: GetEnvOrDefault("DEVSY_TSNET_SCHEME", "https"),
		Host:   s.config.PlatformHost,
	}
	if err := CheckDerpConnection(ctx, baseURL, s.config.Insecure); err != nil {
		return nil, fmt.Errorf("failed to verify DERP connection: %w", err)
	}
	return baseURL, nil
}

// initTsServer initializes the TSNet server.
func (s *WorkspaceServer) initTsServer(ctx context.Context, controlURL *url.URL) error {
	store, _ := mem.New(s.config.LogF, "")
	if s.config.Insecure {
		envknob.Setenv("TS_DEBUG_TLS_DIAL_INSECURE_SKIP_VERIFY", "true")
	}
	log.Infof("connecting to control URL - %s/coordinator/", controlURL.String())
	s.tsServer = &tsnet.Server{
		Hostname:   s.config.WorkspaceHost,
		Logf:       s.config.LogF,
		ControlURL: controlURL.String() + "/coordinator/",
		AuthKey:    s.config.AccessKey,
		Dir:        s.config.RootDir,
		Ephemeral:  true,
		Store:      store,
	}
	if _, err := s.tsServer.Up(ctx); err != nil {
		return err
	}
	return nil
}

// parseWorkspaceHostname extracts workspace and project names from the hostname.
func (s *WorkspaceServer) parseWorkspaceHostname() (workspace, project string, err error) {
	parts := strings.Split(s.config.WorkspaceHost, ".")
	if len(parts) < 4 {
		return "", "", fmt.Errorf("invalid workspace hostname format: %s", s.config.WorkspaceHost)
	}
	return parts[1], parts[2], nil
}

// watchNetmap persists the tailnet status to netmap.json for debugging,
// throttled to once per netMapCooldown.
func (s *WorkspaceServer) watchNetmap(ctx context.Context, lc *local.Client) {
	lastUpdate := time.Now()
	err := WatchNetmap(ctx, lc, func(status *ipnstate.Status) {
		if time.Since(lastUpdate) < netMapCooldown {
			return
		}
		lastUpdate = time.Now()
		PersistNetmapStatus(s.config.RootDir, status)
	})
	if err != nil {
		log.Errorf("failed to watch netmap: %v", err)
	}
}

// startListeners creates and starts the SSH and HTTP reverse proxy listeners.
func (s *WorkspaceServer) startListeners(ctx context.Context, runner *runnerClient) error {
	log.Infof("starting SSH listener")
	sshListener, err := s.createListener(fmt.Sprintf(":%d", sshServer.DefaultUserPort))
	if err != nil {
		return err
	}

	log.Infof("starting HTTP reverse proxy listener on TSNet port %s", TSPortForwardPort)
	wsListener, err := s.createListener(fmt.Sprintf(":%s", TSPortForwardPort))
	if err != nil {
		return fmt.Errorf("failed to create listener on TS port %s: %w", TSPortForwardPort, err)
	}

	runnerProxyListener, err := s.createRunnerProxySocket()
	if err != nil {
		return err
	}

	s.listeners = append(s.listeners, sshListener, wsListener, runnerProxyListener)

	transport := &http.Transport{DialContext: s.tsServer.Dial}
	go s.serveRunnerProxy(runnerProxyListener, runner, transport)
	go s.servePortForward(wsListener)
	go s.handleSSHConnections(ctx, sshListener)

	return nil
}

// createRunnerProxySocket creates the unix socket the platform runner uses to
// reach the workspace's credential-proxy endpoints.
func (s *WorkspaceServer) createRunnerProxySocket() (net.Listener, error) {
	runnerProxySocket := filepath.Join(s.config.RootDir, RunnerProxySocket)
	log.Infof("starting runner proxy socket on %s", runnerProxySocket)

	_ = os.Remove(runnerProxySocket)
	listener, err := net.Listen("unix", runnerProxySocket)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to create runner proxy socket %s: %w",
			runnerProxySocket,
			err,
		)
	}

	// The daemon runs as the container's default (often root) user, but git/docker
	// commands invoking the credential helper run as whatever devcontainer remoteUser
	// an interactive session su'd to (see pkg/ssh/server), a different UID. All local
	// users must therefore reach this socket.
	chmodErr := os.Chmod(runnerProxySocket, 0o777) // #nosec G302 -- see comment above
	if chmodErr != nil {
		log.Errorf("failed to chmod runner proxy socket %s: %v", runnerProxySocket, chmodErr)
	}

	return listener, nil
}

func (s *WorkspaceServer) serveRunnerProxy(
	listener net.Listener,
	runner *runnerClient,
	transport *http.Transport,
) {
	mux := http.NewServeMux()
	mux.HandleFunc(
		"/git-credentials",
		s.runnerProxyHandler(runner, transport, "workspace-git-credentials"),
	)
	mux.HandleFunc(
		"/docker-credentials",
		s.runnerProxyHandler(runner, transport, "workspace-docker-credentials"),
	)
	serveMux(listener, mux, "runner proxy server error: %v")
}

func (s *WorkspaceServer) servePortForward(listener net.Listener) {
	mux := http.NewServeMux()
	mux.HandleFunc("/portforward", s.httpPortForwardHandler)
	serveMux(listener, mux, fmt.Sprintf("http server error on TS port %s: %%v", TSPortForwardPort))
}

func serveMux(listener net.Listener, mux *http.ServeMux, errFormat string) {
	// #nosec G114 -- internal unix-socket/TSNet listener, not exposed to untrusted networks
	if err := http.Serve(listener, mux); err != nil && err != http.ErrServerClosed {
		log.Errorf(errFormat, err)
	}
}

// createListener creates a listener bound to the TSNet server.
func (s *WorkspaceServer) createListener(addr string) (net.Listener, error) {
	l, err := s.tsServer.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	return l, nil
}

func (s *WorkspaceServer) addConnection() {
	s.connectionCount.Add(1)
}

func (s *WorkspaceServer) removeConnection() {
	s.connectionCount.Add(-1)
}

// runnerProxyHandler builds a reverse-proxy handler that discovers the
// runner peer and forwards the request to path on it.
func (s *WorkspaceServer) runnerProxyHandler(
	runner *runnerClient,
	transport *http.Transport,
	path string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Infof("received %s request from %s", path, r.RemoteAddr)

		runnerHost, err := runner.discoverRunner(r.Context())
		if err != nil {
			http.Error(w, "failed to discover runner", http.StatusInternalServerError)
			return
		}

		parsedURL, err := url.Parse(runner.url(runnerHost, path))
		if err != nil {
			http.Error(w, "failed to parse runner URL", http.StatusInternalServerError)
			return
		}

		proxy := &httputil.ReverseProxy{
			Transport: transport,
			Rewrite: func(pr *httputil.ProxyRequest) {
				dest := *parsedURL
				pr.Out.URL = &dest
				pr.Out.Host = dest.Host
				pr.Out.Header.Set("Authorization", "Bearer "+runner.accessKey)
				addForwardedFor(pr)
			},
		}
		proxy.ServeHTTP(w, r)
	}
}

// httpPortForwardHandler is the HTTP reverse proxy handler for workspace.
// It reconstructs the target URL using custom headers and forwards the request.
func (s *WorkspaceServer) httpPortForwardHandler(w http.ResponseWriter, r *http.Request) {
	s.addConnection()
	defer s.removeConnection()
	log.Debugf("httpPortForwardHandler: starting")

	targetPort := r.Header.Get("X-Devsy-Forward-Port")
	baseForwardStr := r.Header.Get("X-Devsy-Forward-Url")
	if targetPort == "" || baseForwardStr == "" {
		http.Error(w, "missing required X-Devsy headers", http.StatusBadRequest)
		return
	}
	log.Debugf(
		"httpPortForwardHandler: received headers: X-Devsy-Forward-Port=%s, X-Devsy-Forward-Url=%s",
		targetPort,
		baseForwardStr,
	)

	parsedURL, err := url.Parse(baseForwardStr)
	if err != nil {
		log.Errorf("httpPortForwardHandler: failed to parse base URL: %v", err)
		http.Error(w, "invalid base forward URL", http.StatusBadRequest)
		return
	}
	parsedURL.Scheme = "http"
	parsedURL.Host = "127.0.0.1:" + targetPort
	log.Debugf("httpPortForwardHandler: final target URL=%s", parsedURL.String())

	proxy := &httputil.ReverseProxy{
		Transport: http.DefaultTransport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			dest := *parsedURL
			pr.Out.URL = &dest
			pr.Out.Host = dest.Host
			// Remove custom headers so they are not forwarded.
			pr.Out.Header.Del("X-Devsy-Forward-Port")
			pr.Out.Header.Del("X-Devsy-Forward-Url")
			pr.Out.Header.Del("X-Devsy-Forward-Authorization")
			addForwardedFor(pr)
		},
	}

	log.Infof(
		"httpPortForwardHandler: final proxied request: %s %s",
		r.Method,
		parsedURL.String(),
	)
	proxy.ServeHTTP(w, r)
}

// addForwardedFor sets X-Forwarded-For on the outbound proxy request.
func addForwardedFor(pr *httputil.ProxyRequest) {
	clientIP, _, err := net.SplitHostPort(pr.In.RemoteAddr)
	if err != nil {
		return
	}
	prior, ok := pr.In.Header["X-Forwarded-For"]
	omit := ok && prior == nil
	if len(prior) > 0 {
		clientIP = strings.Join(prior, ", ") + ", " + clientIP
	}
	if !omit {
		pr.Out.Header.Set("X-Forwarded-For", clientIP)
	}
}

// handleSSHConnections continuously accepts SSH connections and handles each one.
func (s *WorkspaceServer) handleSSHConnections(ctx context.Context, listener net.Listener) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		clientConn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Errorf("failed to accept connection: %v", err)
			continue
		}
		go s.handleSSHConnection(clientConn)
	}
}

// handleSSHConnection proxies the SSH connection to the local backend.
func (s *WorkspaceServer) handleSSHConnection(clientConn net.Conn) {
	s.addConnection()
	defer s.removeConnection()
	defer func() { _ = clientConn.Close() }()

	localAddr := fmt.Sprintf("127.0.0.1:%d", sshServer.DefaultUserPort)
	backendConn, err := net.Dial("tcp", localAddr)
	if err != nil {
		log.Errorf("failed to connect to local address %s: %v", localAddr, err)
		return
	}
	defer func() { _ = backendConn.Close() }()

	go func() {
		defer func() { _ = clientConn.Close() }()
		defer func() { _ = backendConn.Close() }()
		_, err = io.Copy(backendConn, clientConn)
	}()
	_, err = io.Copy(clientConn, backendConn)
}

func (s *WorkspaceServer) sendHeartbeats(ctx context.Context, runner *runnerClient) {
	client := &http.Client{
		Transport: &http.Transport{DialContext: s.tsServer.Dial},
		Timeout:   10 * time.Second,
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.connectionCount.Load() <= 0 {
				continue
			}
			if err := s.sendHeartbeat(ctx, client, runner); err != nil {
				log.Errorf("failed to send heartbeat: %v", err)
			}
		}
	}
}

func (s *WorkspaceServer) sendHeartbeat(
	ctx context.Context,
	client *http.Client,
	runner *runnerClient,
) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	runnerHost, err := runner.discoverRunner(ctx)
	if err != nil {
		return fmt.Errorf("failed to discover runner: %w", err)
	}

	heartbeatURL := runner.url(runnerHost, "heartbeat")
	log.Infof(
		"sending heartbeat to %s, because there are %d active connections",
		heartbeatURL,
		s.connectionCount.Load(),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, heartbeatURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request for %s: %w", heartbeatURL, err)
	}
	req.Header.Set("Authorization", "Bearer "+runner.accessKey)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request to %s failed: %w", heartbeatURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("received response from %s - status: %d", heartbeatURL, resp.StatusCode)
	}
	log.Infof("received response from %s - status: %d", heartbeatURL, resp.StatusCode)
	return nil
}

// runnerClient bundles what's needed to locate the platform runner peer and
// address its endpoints on the tailnet.
type runnerClient struct {
	lc            *local.Client
	accessKey     string
	projectName   string
	workspaceName string
}

// discoverRunner finds the runner peer's hostname from the TSNet status.
func (rc *runnerClient) discoverRunner(ctx context.Context) (string, error) {
	status, err := rc.lc.Status(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get status: %w", err)
	}

	runnerHost, ok := selectRunnerPeer(status.Peer)
	if !ok {
		return "", fmt.Errorf("no active runner found")
	}
	log.Infof("discoverRunner: selected runner = %s", runnerHost)
	return runnerHost, nil
}

// selectRunnerPeer picks the platform runner from the tailnet's peer set,
// identified by a "runner"-suffixed hostname.
func selectRunnerPeer(peers map[key.NodePublic]*ipnstate.PeerStatus) (string, bool) {
	for _, peer := range peers {
		if peer != nil && strings.HasSuffix(peer.HostName, "runner") {
			return peer.HostName, true
		}
	}
	return "", false
}

// url builds the runner-relative URL for path, e.g. "heartbeat" or
// "workspace-git-credentials".
func (rc *runnerClient) url(runnerHost, path string) string {
	return fmt.Sprintf(
		"http://%s.%s/devsy/%s/%s/%s",
		runnerHost,
		DevsyTSNetDomain,
		rc.projectName,
		rc.workspaceName,
		path,
	)
}
