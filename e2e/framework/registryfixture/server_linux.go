//go:build linux

// Package registryfixture owns the bounded Linux registry/proxy and helper
// fixtures used by native provider-consumer tests.
package registryfixture

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const RegistryHost = "registry.invalid"

type Config struct {
	Username string
	Password string
}

type Request struct {
	Method        string `json:"method"`
	Path          string `json:"path"` // normalized route class, never the raw URL
	Status        int    `json:"status"`
	Authenticated bool   `json:"authenticated"`
	Bytes         int64  `json:"bytes"`
	BodyDigest    string `json:"body_digest,omitempty"`
	Digest        string `json:"docker_content_digest,omitempty"`
}

type Snapshot struct {
	ConnectAllowed int       `json:"connect_allowed"`
	ConnectDenied  int       `json:"connect_denied"`
	Authenticated  int       `json:"authenticated_requests"`
	AuthRejected   int       `json:"auth_rejected_requests"`
	Completed      int       `json:"completed_responses"`
	ManifestGets   int       `json:"manifest_gets"`
	BlobGets       int       `json:"blob_gets"`
	ResponseBytes  int64     `json:"response_bytes"`
	Requests       []Request `json:"requests"`
}

// Server owns one TLS registry listener and one CONNECT listener. The proxy
// accepts only the exact registry authority and dials the owned listener IP;
// it never resolves or forwards caller-selected destinations.
type Server struct {
	Authority string
	ProxyURL  string
	CAPEM     []byte
	Username  string
	Password  string

	registry *http.Server
	regLn    net.Listener
	proxy    *http.Server
	proxyLn  net.Listener
	mu       sync.Mutex
	stats    Snapshot
	close    sync.Once
	closeErr error
	tunnelMu sync.Mutex
	closing  bool
	active   int
	idle     chan struct{}
	tunnels  map[net.Conn]struct{}
}

func Start(next http.Handler, cfg Config) (*Server, error) {
	if next == nil || cfg.Username == "" || cfg.Password == "" {
		return nil, fmt.Errorf("registry handler and fixture credentials are required")
	}
	regLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen owned registry: %w", err)
	}
	cert, caPEM, err := makeCertificate()
	if err != nil {
		return nil, errors.Join(err, closeListener(regLn))
	}
	s := &Server{
		Authority: RegistryHost + ":" + fmt.Sprint(regLn.Addr().(*net.TCPAddr).Port),
		CAPEM:     caPEM, Username: cfg.Username, Password: cfg.Password, regLn: regLn,
		tunnels: make(map[net.Conn]struct{}), idle: make(chan struct{}),
	}
	close(s.idle)
	regTLS := tls.NewListener(regLn, &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	})
	s.registry = &http.Server{Handler: s.authenticated(next), ReadHeaderTimeout: 5 * time.Second}
	s.tunnelMu.Lock()
	s.trackStartLocked()
	s.tunnelMu.Unlock()
	go func() { defer s.trackDone(); _ = s.registry.Serve(regTLS) }()

	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.tunnelMu.Lock()
		s.closing = true
		s.tunnelMu.Unlock()
		registryErr := s.registry.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return nil, errors.Join(
			fmt.Errorf("listen owned proxy: %w", err),
			registryErr,
			s.waitIdle(ctx),
			closeListener(regLn),
		)
	}
	s.proxyLn = proxyLn
	s.ProxyURL = "http://" + proxyLn.Addr().String()
	s.proxy = &http.Server{Handler: http.HandlerFunc(s.connect), ReadHeaderTimeout: 5 * time.Second}
	s.tunnelMu.Lock()
	s.trackStartLocked()
	s.tunnelMu.Unlock()
	go func() { defer s.trackDone(); _ = s.proxy.Serve(proxyLn) }()
	return s, nil
}

func isSuccessfulGet(r *http.Request, status int, path, expectedPath string) bool {
	return r.Method == http.MethodGet && status >= 200 && status < 300 && path == expectedPath
}

func routeClass(path string) string {
	switch {
	case path == "/v2/":
		return "v2"
	case strings.Contains(path, "/manifests/"):
		return "manifests"
	case strings.Contains(path, "/blobs/uploads/"):
		return "uploads"
	case strings.Contains(path, "/blobs/"):
		return "blobs"
	default:
		return "other"
	}
}

type countWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
	digest hash.Hash
}

func (w *countWriter) WriteHeader(code int) {
	if w.status != 0 {
		return
	}
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *countWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		// Match net/http's implicit status before forwarding the first body write.
		// A later WriteHeader is ignored by the underlying writer and must be
		// ignored by our observation too.
		w.WriteHeader(http.StatusOK)
	}
	n, e := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	if n > 0 && w.digest != nil {
		_, _ = w.digest.Write(p[:n])
	}
	return n, e
}

func (w *countWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *countWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// hijackConnect is split out to keep CONNECT tunneling explicit and small.
func hijackConnect(w http.ResponseWriter) (net.Conn, error) {
	h, ok := w.(http.Hijacker)
	if !ok {
		return nil, fmt.Errorf("HTTP server does not support hijacking")
	}
	c, rw, err := h.Hijack()
	if err != nil {
		return nil, err
	}
	if _, err = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		_ = c.Close()
		return nil, err
	}
	if err = rw.Flush(); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (s *Server) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.stats
	out.Requests = append([]Request(nil), s.stats.Requests...)
	return out
}

// Close shuts down both listeners and joins servers and active tunnels.
func (s *Server) Close(ctx context.Context) error {
	s.close.Do(func() {
		s.tunnelMu.Lock()
		s.closing = true
		owned := make([]net.Conn, 0, len(s.tunnels))
		for conn := range s.tunnels {
			owned = append(owned, conn)
		}
		idle := s.idle
		s.tunnelMu.Unlock()

		// Closing registered tunnels first releases both copy directions; the
		// close fence prevents any later CONNECT from registering new work.
		var tunnelCloseErr error
		for _, conn := range owned {
			if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				tunnelCloseErr = errors.Join(tunnelCloseErr, err)
			}
		}
		s.closeErr = errors.Join(tunnelCloseErr,
			s.registry.Shutdown(ctx), s.proxy.Shutdown(ctx),
			s.registry.Close(), s.proxy.Close(), closeListener(s.regLn), closeListener(s.proxyLn),
		)
		select {
		case <-idle:
		case <-ctx.Done():
			s.closeErr = errors.Join(s.closeErr, ctx.Err())
		}
	})
	return s.closeErr
}

func (s *Server) trackStartLocked() {
	if s.active == 0 {
		s.idle = make(chan struct{})
	}
	s.active++
}

func (s *Server) trackDone() {
	s.tunnelMu.Lock()
	s.active--
	if s.active == 0 {
		close(s.idle)
	}
	s.tunnelMu.Unlock()
}

func (s *Server) waitIdle(ctx context.Context) error {
	s.tunnelMu.Lock()
	idle := s.idle
	s.tunnelMu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func closeListener(ln net.Listener) error {
	err := ln.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func makeCertificate() (tls.Certificate, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber:          bigIntSerial(),
		Subject:               pkix.Name{CommonName: RegistryHost},
		DNSNames:              []string{RegistryHost},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(2 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(
		&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)},
	)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	return cert, certPEM, err
}

func bigIntSerial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}

func (s *Server) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.tunnelMu.Lock()
		if s.closing {
			s.tunnelMu.Unlock()
			http.Error(w, "owned fixture is closing", http.StatusServiceUnavailable)
			return
		}
		s.trackStartLocked()
		s.tunnelMu.Unlock()
		defer s.trackDone()
		path := routeClass(r.URL.Path)
		authenticated := s.validBasic(r)
		cw := &countWriter{ResponseWriter: w, digest: sha256.New()}
		defer s.recordResponse(r, cw, path, authenticated)
		if !authenticated {
			w.Header().Set("WWW-Authenticate", `Basic realm="case-a-owned-registry"`)
			http.Error(cw, "authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(cw, r)
	})
}

func (s *Server) recordResponse(r *http.Request, cw *countWriter, path string, authenticated bool) {
	status := cw.status
	if status == 0 {
		status = http.StatusOK
	}
	bodyDigest := ""
	if cw.bytes > 0 {
		bodyDigest = fmt.Sprintf("sha256:%x", cw.digest.Sum(nil))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if authenticated {
		s.stats.Authenticated++
	} else {
		s.stats.AuthRejected++
	}
	s.stats.Completed++
	s.stats.ResponseBytes += cw.bytes
	if authenticated && isSuccessfulGet(r, status, path, "manifests") {
		s.stats.ManifestGets++
	}
	if authenticated && isSuccessfulGet(r, status, path, "blobs") {
		s.stats.BlobGets++
	}
	s.stats.Requests = append(s.stats.Requests, Request{
		Method: r.Method, Path: path, Status: status, Authenticated: authenticated,
		Bytes: cw.bytes, BodyDigest: bodyDigest, Digest: cw.Header().Get("Docker-Content-Digest"),
	})
}

func (s *Server) validBasic(r *http.Request) bool {
	u, p, ok := r.BasicAuth()
	return ok && u == s.Username && p == s.Password
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	if !s.registerConnectHandler() {
		http.Error(w, "owned fixture is closing", http.StatusServiceUnavailable)
		return
	}
	defer s.trackDone()
	if !s.ownsConnectTarget(r) {
		s.denyConnect(w)
		return
	}
	client, err := hijackConnect(w)
	if err != nil {
		return
	}
	s.openTunnel(client)
}

func (s *Server) registerConnectHandler() bool {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	if s.closing {
		return false
	}
	// Register before Shutdown can ignore a handler that later hijacks.
	s.trackStartLocked()
	return true
}

func (s *Server) ownsConnectTarget(r *http.Request) bool {
	return r.Method == http.MethodConnect && r.Host == s.Authority
}

func (s *Server) denyConnect(w http.ResponseWriter) {
	s.mu.Lock()
	s.stats.ConnectDenied++
	s.mu.Unlock()
	http.Error(w, "CONNECT destination is not the owned fixture registry", http.StatusForbidden)
}

func (s *Server) openTunnel(client net.Conn) {
	upstream, err := net.DialTimeout("tcp", s.regLn.Addr().String(), 3*time.Second)
	if err != nil {
		_ = client.Close()
		return
	}
	s.mu.Lock()
	s.stats.ConnectAllowed++
	s.mu.Unlock()
	if !s.registerTunnel(client, upstream) {
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	go s.copyTunnel(client, upstream)
}

func (s *Server) registerTunnel(client, upstream net.Conn) bool {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	if s.closing {
		return false
	}
	s.tunnels[client] = struct{}{}
	s.tunnels[upstream] = struct{}{}
	s.trackStartLocked() // register the tunnel under the same close fence
	return true
}

func (s *Server) copyTunnel(client, upstream net.Conn) {
	defer s.trackDone()
	defer func() {
		_ = client.Close()
		_ = upstream.Close()
		s.tunnelMu.Lock()
		delete(s.tunnels, client)
		delete(s.tunnels, upstream)
		s.tunnelMu.Unlock()
	}()
	var copyWG sync.WaitGroup
	copyWG.Go(func() {
		_, _ = io.Copy(upstream, client)
		if c, ok := upstream.(*net.TCPConn); ok {
			_ = c.CloseWrite()
		}
	})
	_, _ = io.Copy(client, upstream)
	copyWG.Wait()
}
