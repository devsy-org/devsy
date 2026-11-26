//go:build linux

package ghfixture

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var Hosts = []string{githubHost, "enterprise.one.test", "enterprise.two.test"}

const CertificatePath = "/usr/local/share/ca-certificates/devsy-gh-fixture.crt"

type Tokens struct {
	Forwarded string `json:"forwarded"`
	Local     string `json:"local"`
	Second    string `json:"second"`
	Invalid   string `json:"invalid"`
	Refresh   string `json:"refresh"`
}

type Accounts map[string]Tokens

type Observation struct {
	Host   string `json:"host"`
	Route  string `json:"route"`
	User   string `json:"user"`
	Status int    `json:"status"`
}

type APIServer struct {
	root         string
	accounts     Accounts
	certificate  tls.Certificate
	listener     net.Listener
	server       *http.Server
	mu           sync.Mutex
	connections  map[net.Conn]bool
	observations []Observation
	wg           sync.WaitGroup
	closing      bool
}

func StartAPI(root, certificatePath, address string, accounts Accounts) (*APIServer, error) {
	certificate, pub, err := fixtureCertificate()
	if err != nil {
		return nil, err
	}
	// #nosec G306 -- This is the public fixture trust certificate, shared with the non-root guest.
	if err := os.WriteFile(certificatePath, pub, 0o644); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp4", address)
	if err != nil {
		return nil, err
	}
	s := &APIServer{
		root:        root,
		accounts:    accounts,
		certificate: certificate,
		listener:    ln,
		connections: map[net.Conn]bool{},
	}
	s.server = &http.Server{
		Handler:           http.HandlerFunc(s.connect),
		ReadHeaderTimeout: 2 * time.Second,
	}
	s.wg.Go(func() { _ = s.server.Serve(ln) })
	return s, nil
}

func (s *APIServer) Address() string { return s.listener.Addr().String() }

func (s *APIServer) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closing = true
	for conn := range s.connections {
		_ = conn.Close()
	}
	s.mu.Unlock()
	err := s.server.Close()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return err
	case <-ctx.Done():
		return errors.Join(err, ctx.Err())
	}
}

func canonicalHost(authority string) string {
	host, port, err := net.SplitHostPort(authority)
	if err == nil {
		if port != "443" {
			return ""
		}
		authority = host
	}
	return strings.ToLower(authority)
}

func apiHost(authority string) string {
	host := canonicalHost(authority)
	if host == githubAPIHost {
		return githubHost
	}
	return host
}

func allowedAuthority(authority string) bool {
	switch canonicalHost(authority) {
	case githubHost, githubAPIHost, "enterprise.one.test", "enterprise.two.test":
		return true
	default:
		return false
	}
}

func (s *APIServer) connect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect || !allowedAuthority(r.Host) ||
		!strings.HasSuffix(r.Host, ":443") {
		http.Error(w, "denied", http.StatusForbidden)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "unavailable", 500)
		return
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		_ = conn.Close()
		return
	}
	s.connections[conn] = true
	s.wg.Add(1)
	s.mu.Unlock()
	defer func() { _ = conn.Close(); s.mu.Lock(); delete(s.connections, conn); s.mu.Unlock(); s.wg.Done() }()
	_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	if rw.Flush() != nil {
		return
	}
	wrapped := tls.Server(
		&bufferedConnection{Conn: conn, reader: rw.Reader},
		&tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{s.certificate}},
	)
	s.serveTunnel(wrapped, canonicalHost(r.Host))
}

type bufferedConnection struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConnection) Read(b []byte) (int, error) { return c.reader.Read(b) }

func (s *APIServer) serveTunnel(conn *tls.Conn, authority string) {
	reader := bufio.NewReader(conn)
	for {
		_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
		r, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		w := httptest.NewRecorder()
		if canonicalHost(r.Host) != authority {
			http.Error(w, "authority mismatch", http.StatusForbidden)
		} else {
			s.handleAPI(w, r)
		}
		_ = r.Body.Close()
		response := w.Result()
		response.ContentLength = int64(w.Body.Len())
		if response.Write(conn) != nil {
			_ = response.Body.Close()
			return
		}
		_ = response.Body.Close()
		if r.Close {
			return
		}
	}
}

func (s *APIServer) userFor(host, token string) string {
	record, ok := s.accounts[apiHost(host)]
	if !ok || token == "" {
		return ""
	}
	switch token {
	case record.Forwarded, record.Refresh, localRaceToken(record):
		return hostUser
	case record.Local:
		return localUser
	case record.Second:
		return secondUser
	default:
		return ""
	}
}

func (s *APIServer) handleAPI(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
	_, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	user := s.userFor(r.Host, token)
	if user == localUser && exists(filepath.Join(s.root, "revoke-local")) {
		user = ""
	}
	code, body := s.apiResponse(r, user)
	s.mu.Lock()
	s.observations = append(
		s.observations,
		Observation{Host: apiHost(r.Host), Route: apiRoute(r.URL.Path), User: user, Status: code},
	)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-OAuth-Scopes", "repo, read:org, gist")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func (s *APIServer) apiResponse(r *http.Request, user string) (int, any) {
	if status, body := s.oauthResponse(r); status != 0 {
		return status, body
	}
	if user == "" {
		return 401, map[string]string{"message": "Bad credentials"}
	}
	if user == localUser && exists(filepath.Join(s.root, "forbid-local")) &&
		strings.Contains(r.URL.Path, "/repos/") {
		return 403, map[string]string{"message": "Forbidden"}
	}
	return 200, map[string]any{
		"login": user,
		"id":    123,
		"data":  map[string]any{"viewer": map[string]string{"login": user}},
	}
}

func (s *APIServer) oauthResponse(r *http.Request) (int, any) {
	if r.URL.Path == "/login/device/code" && apiHost(r.Host) == Hosts[0] {
		return 200, map[string]any{
			"device_code":      "fixture-device",
			"user_code":        "fixture-code",
			"verification_uri": "https://github.com/login/device",
			"expires_in":       600,
			"interval":         1,
		}
	}
	if r.URL.Path == "/login/oauth/access_token" && apiHost(r.Host) == Hosts[0] {
		return 200, map[string]string{
			"access_token": s.accounts[Hosts[0]].Refresh,
			"token_type":   "bearer",
			"scope":        "repo,read:org,gist",
		}
	}

	return 0, nil
}

func apiRoute(path string) string {
	switch {
	case strings.Contains(path, "graphql"):
		return "graphql"
	case strings.Contains(path, "/login/"):
		return "oauth"
	case strings.Contains(path, "/repos/"):
		return "repository"
	default:
		return "user"
	}
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func fixtureCertificate() (tls.Certificate, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: githubHost},
		DNSNames:     append([]string{githubAPIHost}, Hosts...),
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	pub := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	certificate, err := tls.X509KeyPair(
		pub,
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}),
	)
	return certificate, pub, err
}
