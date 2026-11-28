package egress

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
	"github.com/stretchr/testify/suite"
)

const (
	publicHostPort = "8.8.8.8:443"
	bindLoopback   = "127.0.0.1:0"
	zonedAuthority = "[fe80::1%25eth0]:443"
	localAuthority = "localhost:443"
	testSession    = "p-pool-abcdefghij"
	denyBody       = "egress denied for session " + testSession + "\n"
	badGatewayBody = "egress upstream unavailable for session " + testSession + "\n"
	tunnelHost     = "example.com"
	tunnelAuth     = tunnelHost + ":443"
	waitFor        = 5 * time.Second
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type recorderStub struct {
	mu      sync.Mutex
	denied  []Denial
	allowed []Allow
	// refuse makes Allowed report that the pool did not get the decision.
	refuse bool
}

func (r *recorderStub) Denied(d Denial) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.denied = append(r.denied, d)
}

func (r *recorderStub) Allowed(a Allow) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.allowed = append(r.allowed, a)
	return !r.refuse
}

func (r *recorderStub) snapshot() ([]Denial, []Allow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Denial(nil), r.denied...), append([]Allow(nil), r.allowed...)
}

type dialLog struct {
	mu      sync.Mutex
	targets []netip.AddrPort
}

func (d *dialLog) add(t netip.AddrPort) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.targets = append(d.targets, t)
}

func (d *dialLog) all() []netip.AddrPort {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]netip.AddrPort(nil), d.targets...)
}

// rawUpstream is a TCP server that stands in for the destination. By default
// it records everything a connection sends until the connection ends, then
// writes reply and closes.
type rawUpstream struct {
	ln       net.Listener
	reply    []byte
	handle   func(net.Conn)
	mu       sync.Mutex
	received [][]byte
	finished int
}

func newRawUpstream(tb testing.TB, reply []byte) *rawUpstream {
	tb.Helper()
	return newHandledUpstream(tb, reply, nil)
}

func newHandledUpstream(tb testing.TB, reply []byte, handle func(net.Conn)) *rawUpstream {
	tb.Helper()
	ln, err := net.Listen("tcp", bindLoopback)
	if err != nil {
		tb.Fatal(err)
	}
	u := &rawUpstream{ln: ln, reply: reply, handle: handle}
	tb.Cleanup(func() { _ = ln.Close() })
	go u.accept()
	return u
}

func (u *rawUpstream) accept() {
	for {
		conn, err := u.ln.Accept()
		if err != nil {
			return
		}
		go u.serve(conn)
	}
}

func (u *rawUpstream) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	defer func() {
		u.mu.Lock()
		u.finished++
		u.mu.Unlock()
	}()
	if u.handle != nil {
		u.handle(conn)
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(waitFor))
	data, _ := io.ReadAll(conn)
	u.mu.Lock()
	u.received = append(u.received, data)
	u.mu.Unlock()
	_, _ = conn.Write(u.reply)
}

func (u *rawUpstream) bytes() [][]byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([][]byte(nil), u.received...)
}

func (u *rawUpstream) done() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.finished
}

type envCfg struct {
	upstream     string
	rules        []poolconfig.AllowRule
	answers      []netip.Addr
	lookupErr    error
	connectErr   error
	maxConns     int
	idle         time.Duration
	lifetime     time.Duration
	helloTimeout time.Duration
	headerWait   time.Duration
	healthy      func() bool
	// refuseAllow makes the recorder report that no allow was delivered.
	refuseAllow bool
}

type proxyEnv struct {
	addr    string
	rec     *recorderStub
	logs    *syncBuffer
	dialed  *dialLog
	proxy   *Proxy
	cancel  context.CancelFunc
	done    chan error
	stopped sync.Once
	result  error
}

func (e *proxyEnv) stop() error {
	e.stopped.Do(func() {
		e.cancel()
		select {
		case e.result = <-e.done:
		case <-time.After(waitFor):
			e.result = errors.New("Serve did not return")
		}
	})
	return e.result
}

type ProxySuite struct {
	suite.Suite
}

func TestProxySuite(t *testing.T) {
	suite.Run(t, new(ProxySuite))
}

func startProxy(s *ProxySuite, cfg envCfg) *proxyEnv {
	env := &proxyEnv{
		rec:    &recorderStub{refuse: cfg.refuseAllow},
		logs:   &syncBuffer{},
		dialed: &dialLog{},
		done:   make(chan error, 1),
	}
	if cfg.rules == nil {
		cfg.rules = []poolconfig.AllowRule{host(tunnelHost), host(hostWild)}
	}
	if cfg.answers == nil && cfg.lookupErr == nil {
		cfg.answers = addrs(publicAddr)
	}
	dialer := &Dialer{
		policy:   newPolicy(s.T(), cfg.rules, "172.17.0.0/16"),
		resolver: &fakeResolver{answers: cfg.answers, err: cfg.lookupErr},
		budget:   dialBudget,
		connect: func(ctx context.Context, t netip.AddrPort, verify func(string) error) (net.Conn, error) {
			env.dialed.add(t)
			if err := verify(t.String()); err != nil {
				return nil, err
			}
			if cfg.connectErr != nil {
				return nil, cfg.connectErr
			}
			return (&net.Dialer{}).DialContext(ctx, "tcp", cfg.upstream)
		},
	}
	proxy, err := NewProxy(Config{
		Bind:      bindLoopback,
		SessionID: testSession,
		Dialer:    dialer,
		Recorder:  env.rec,
		Logger: slog.New(
			slog.NewTextHandler(env.logs, &slog.HandlerOptions{Level: slog.LevelDebug}),
		),
		Healthy:     cfg.healthy,
		MaxConns:    cfg.maxConns,
		IdleTimeout: cfg.idle,
		MaxLifetime: cfg.lifetime,
	})
	s.Require().NoError(err)
	if cfg.helloTimeout > 0 {
		proxy.helloTimeout = cfg.helloTimeout
	}
	if cfg.headerWait > 0 {
		proxy.headerTimeout = cfg.headerWait
	}
	addr, err := proxy.Listen()
	s.Require().NoError(err)
	ctx, cancel := context.WithCancel(context.Background())
	env.proxy, env.addr, env.cancel = proxy, addr.String(), cancel
	go func() { env.done <- proxy.Serve(ctx) }()
	s.T().Cleanup(func() { s.NoError(env.stop()) })
	return env
}

func connectLine(authority string) string {
	return "CONNECT " + authority + " HTTP/1.1\r\nHost: " + authority + "\r\n\r\n"
}

type client struct {
	conn net.Conn
	in   *bufio.Reader
}

func dialProxy(s *ProxySuite, env *proxyEnv) *client {
	conn, err := net.DialTimeout("tcp", env.addr, waitFor)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(waitFor))
	return &client{conn: conn, in: bufio.NewReader(conn)}
}

func (c *client) send(s *ProxySuite, data string) {
	_, err := io.WriteString(c.conn, data)
	s.Require().NoError(err)
}

func (c *client) response(s *ProxySuite, method string) (*http.Response, string) {
	resp, err := http.ReadResponse(c.in, &http.Request{Method: method})
	s.Require().NoError(err)
	body, err := io.ReadAll(resp.Body)
	s.Require().NoError(err)
	s.Require().NoError(resp.Body.Close())
	return resp, string(body)
}

// request sends one raw request on a new connection and reads the answer.
func doRequest(s *ProxySuite, env *proxyEnv, raw string) (*http.Response, string) {
	c := dialProxy(s, env)
	c.send(s, raw)
	return c.response(s, http.MethodConnect)
}

// tunnel opens a CONNECT and requires the 200.
func openTunnel(s *ProxySuite, env *proxyEnv, authority string) *client {
	c := dialProxy(s, env)
	c.send(s, connectLine(authority))
	c.established(s)
	return c
}

// established reads the bodyless 200 that opens a tunnel. http.ReadResponse
// would wait for a body that never ends.
func (c *client) established(s *ProxySuite) {
	status, err := c.in.ReadString('\n')
	s.Require().NoError(err)
	s.Require().Contains(status, " 200 ")
	line, err := c.in.ReadString('\n')
	s.Require().NoError(err)
	s.Require().Equal("\r\n", line)
}

// closed waits for the proxy to end the connection.
func (c *client) closed(s *ProxySuite) {
	buf := make([]byte, 512)
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(waitFor))
		_, err := c.in.Read(buf)
		if err == nil {
			continue
		}
		s.False(errors.Is(err, os.ErrDeadlineExceeded), "the proxy left the connection open")
		return
	}
}

func expectDenials(s *ProxySuite, env *proxyEnv, want ...Reason) []Denial {
	s.Eventually(func() bool {
		denied, _ := env.rec.snapshot()
		return len(denied) >= len(want)
	}, waitFor, 10*time.Millisecond)
	denied, allowed := env.rec.snapshot()
	s.Empty(allowed)
	s.Require().Len(denied, len(want))
	for i, reason := range want {
		s.Equal(reason, denied[i].Reason)
	}
	return denied
}

func tlsUpstream(s *ProxySuite) (*httptest.Server, *x509.CertPool) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello from upstream")
	}))
	s.T().Cleanup(server.Close)
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	return server, pool
}

func (s *ProxySuite) TestATunnelCarriesARealTLSSession() {
	server, pool := tlsUpstream(s)
	env := startProxy(s, envCfg{upstream: server.Listener.Addr().String()})
	c := openTunnel(s, env, "Example.COM.:443")
	conn := tls.Client(
		c.conn,
		&tls.Config{ServerName: tunnelHost, RootCAs: pool, MinVersion: tls.VersionTLS12},
	)
	s.Require().NoError(conn.Handshake())
	_, err := io.WriteString(
		conn,
		"GET / HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n",
	)
	s.Require().NoError(err)
	body, _ := io.ReadAll(conn)
	s.Contains(string(body), "hello from upstream")

	s.Equal([]netip.AddrPort{netip.MustParseAddrPort(publicAddr + ":443")}, env.dialed.all())
	_, allowed := env.rec.snapshot()
	s.Equal([]Target{{Host: tunnelHost, Port: 443}}, targetsOf(allowed))
	denied, _ := env.rec.snapshot()
	s.Empty(denied)
}

func (s *ProxySuite) TestEveryRefusalReadsTheSame() {
	longHost := strings.Repeat("a", 300) + ".com:443"
	for authority, want := range map[string]Reason{
		"evil.example.org:443":         NotAllowed,
		tunnelHost + ":8443":           NotAllowed,
		publicHostPort:                 IPLiteral,
		"169.254.169.254:80":           IPLiteral,
		"[::1]:443":                    IPLiteral,
		zonedAuthority:                 IPLiteral,
		tunnelHost:                     BadRequest,
		localAuthority:                 BadRequest,
		tunnelHost + ":0":              BadRequest,
		tunnelHost + ":65536":          BadRequest,
		tunnelHost + ":0443":           BadRequest,
		longHost:                       BadRequest,
		"metadata.google.internal:443": BadRequest,
	} {
		env := startProxy(s, envCfg{})
		resp, body := doRequest(s, env, connectLine(authority))
		s.Equal(http.StatusForbidden, resp.StatusCode, authority)
		s.Equal(denyBody, body, authority)
		s.True(resp.Close, authority)
		s.Equal("text/plain; charset=utf-8", resp.Header.Get("Content-Type"), authority)
		s.Empty(env.dialed.all(), authority)
		expectDenials(s, env, want)
	}
}

// The http server rejects these before the handler runs. The answer is a
// fixed 400 that does not repeat the request, and nothing is dialed.
func (s *ProxySuite) TestMalformedTargetsAreRefusedByTheServer() {
	env := startProxy(s, envCfg{})
	malformed := []string{
		"user@" + tunnelHost + ":443",
		"tok_SECRET-123@evil.com:443",
		tunnelHost + ":+443",
		"a b.com:443",
	}
	for _, authority := range malformed {
		resp, body := doRequest(s, env, connectLine(authority))
		s.Equal(http.StatusBadRequest, resp.StatusCode, authority)
		s.NotContains(body, "SECRET", authority)
		s.NotContains(body, "evil", authority)
		s.NotContains(body, tunnelHost, authority)
	}
	s.Empty(env.dialed.all())
}

func (s *ProxySuite) TestResolvedAddressRefusalsReadTheSame() {
	for name, cfg := range map[string]struct {
		answers []netip.Addr
		err     error
		want    Reason
	}{
		"loopback":      {addrs(loopback4), nil, ReservedRange},
		"metadata":      {addrs(metadataIP), nil, ReservedRange},
		"mapped":        {addrs("::ffff:127.0.0.1"), nil, ReservedRange},
		"private":       {addrs(privateIP), nil, PrivateRange},
		"pool network":  {addrs("172.17.0.9"), nil, PoolNetwork},
		"mixed":         {addrs(publicAddr, loopback4), nil, ReservedRange},
		"lookup failed": {nil, errors.New("no such host"), ResolveFailed},
		"no answers":    {[]netip.Addr{}, nil, ResolveFailed},
	} {
		env := startProxy(s, envCfg{answers: cfg.answers, lookupErr: cfg.err})
		resp, body := doRequest(s, env, connectLine(tunnelAuth))
		s.Equal(http.StatusForbidden, resp.StatusCode, name)
		s.Equal(denyBody, body, name)
		s.Empty(env.dialed.all(), name)
		denied := expectDenials(s, env, cfg.want)
		s.Equal(Target{Host: tunnelHost, Port: 443}, denied[0].Target, name)
	}
}

func (s *ProxySuite) TestAnUnexpectedConnectFailureIsAFixed502() {
	detail := errors.New("dial tcp 8.8.8.8:443: i/o timeout secret-detail")
	env := startProxy(s, envCfg{connectErr: detail})
	resp, body := doRequest(s, env, connectLine(tunnelAuth))
	s.Equal(http.StatusBadGateway, resp.StatusCode)
	s.Equal(badGatewayBody, body)
	s.NotContains(body, publicAddr)
	s.NotContains(body, "secret-detail")
	s.Contains(env.logs.String(), "secret-detail")
	denied, allowed := env.rec.snapshot()
	s.Empty(denied, "a failed connect is not a policy decision")
	s.Empty(allowed)
}

func (s *ProxySuite) TestOnlyConnectIsAccepted() {
	env := startProxy(s, envCfg{})
	for _, raw := range []string{
		"GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\n\r\n",
		"GET / HTTP/1.1\r\nHost: example.com\r\n\r\n",
		"POST http://example.com/ HTTP/1.1\r\nHost: example.com\r\nContent-Length: 0\r\n\r\n",
	} {
		c := dialProxy(s, env)
		c.send(s, raw+raw)
		resp, body := c.response(s, http.MethodGet)
		s.Equal(http.StatusMethodNotAllowed, resp.StatusCode)
		s.Equal("CONNECT", resp.Header.Get("Allow"))
		s.True(resp.Close, "the refusal must not keep the connection open")
		s.NotContains(body, "example.com")
		c.closed(s)
	}
	s.Empty(env.dialed.all())
}

func (s *ProxySuite) TestAStalledRequestIsClosed() {
	env := startProxy(s, envCfg{headerWait: 200 * time.Millisecond})
	c := dialProxy(s, env)
	c.send(s, "CONNECT "+tunnelHost)
	start := time.Now()
	c.closed(s)
	s.Less(time.Since(start), waitFor)
	s.Empty(env.dialed.all())
}

func (s *ProxySuite) TestOversizedHeadersAreRefused() {
	env := startProxy(s, envCfg{})
	raw := "CONNECT " + tunnelAuth + " HTTP/1.1\r\nHost: x\r\nX-Pad: " + strings.Repeat(
		"a",
		20000,
	) + "\r\n\r\n"
	resp, _ := doRequest(s, env, raw)
	s.Equal(http.StatusRequestHeaderFieldsTooLarge, resp.StatusCode)
	s.Empty(env.dialed.all())
}

func (s *ProxySuite) TestBadClientHellosAreClosedAndNothingIsForwarded() {
	tooBig := []byte{recordHandshake, 3, 1, 0x40, 0x01}
	for name, c := range map[string]struct {
		data   []byte
		reason Reason
	}{
		"other name":     {helloRecord(sniExt("other.example.com")), SNIMismatch},
		"no name":        {helloRecord(), SNIMissing},
		"address name":   {helloRecord(sniExt(publicAddr)), SNIMismatch},
		"two names":      {helloRecord(sniExt(tunnelHost, tunnelHost)), SNIMismatch},
		"two extensions": {helloRecord(sniExt(tunnelHost), sniExt(tunnelHost)), SNIMismatch},
		"ech":            {helloRecord(sniExt(tunnelHost), ext(extECH, []byte{0})), BadRequest},
		"esni":           {helloRecord(ext(extESNI, nil), sniExt(tunnelHost)), BadRequest},
		"http":           {[]byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"), BadRequest},
		"ssh":            {[]byte("SSH-2.0-OpenSSH_9.6\r\n"), BadRequest},
		"record too big": {tooBig, BadRequest},
		"split hello":    {append(record(goodFields(sniExt(tunnelHost)).body()[:20]), 0), BadRequest},
	} {
		up := newRawUpstream(s.T(), nil)
		env := startProxy(s, envCfg{upstream: up.ln.Addr().String()})
		c1 := openTunnel(s, env, tunnelAuth)
		c1.send(s, string(c.data))
		c1.closed(s)
		expectDenials(s, env, c.reason)
		s.Eventually(func() bool { return up.done() == 1 }, waitFor, 10*time.Millisecond, name)
		s.Equal([][]byte{{}}, up.bytes(), name+": nothing may reach the upstream")
		s.Len(env.dialed.all(), 1, name)
	}
}

func (s *ProxySuite) TestSilenceAfterTheTunnelOpensIsClosed() {
	up := newRawUpstream(s.T(), nil)
	env := startProxy(
		s,
		envCfg{upstream: up.ln.Addr().String(), helloTimeout: 200 * time.Millisecond},
	)
	c := openTunnel(s, env, tunnelAuth)
	start := time.Now()
	c.closed(s)
	s.GreaterOrEqual(time.Since(start), 150*time.Millisecond)
	expectDenials(s, env, BadRequest)
	s.Eventually(func() bool { return up.done() == 1 }, waitFor, 10*time.Millisecond)
	s.Equal([][]byte{{}}, up.bytes())
}

func (s *ProxySuite) TestBytesAfterTheHelloReachTheUpstream() {
	up := newRawUpstream(s.T(), []byte("server says bye"))
	env := startProxy(s, envCfg{upstream: up.ln.Addr().String()})
	c := openTunnel(s, env, tunnelAuth)
	hello := helloRecord(sniExt(tunnelHost))
	changeCipherSpec := []byte{0x14, 3, 3, 0, 1, 1}
	c.send(s, string(append(append([]byte{}, hello...), changeCipherSpec...)))
	c.send(s, "later bytes")
	s.Require().NoError(c.conn.(*net.TCPConn).CloseWrite())
	reply, err := io.ReadAll(c.in)
	s.Require().NoError(err)
	s.Equal(
		"server says bye",
		string(reply),
		"the upstream can still answer after the client half-closes",
	)
	s.Eventually(func() bool { return up.done() == 1 }, waitFor, 10*time.Millisecond)
	s.Equal(string(hello)+string(changeCipherSpec)+"later bytes", string(up.bytes()[0]))
	_, allowed := env.rec.snapshot()
	s.Equal([]Target{{Host: tunnelHost, Port: 443}}, targetsOf(allowed))
}

func (s *ProxySuite) TestAnIdleTunnelIsClosed() {
	up := newRawUpstream(s.T(), nil)
	env := startProxy(s, envCfg{upstream: up.ln.Addr().String(), idle: 300 * time.Millisecond})
	c := openTunnel(s, env, tunnelAuth)
	c.send(s, string(helloRecord(sniExt(tunnelHost))))
	start := time.Now()
	c.closed(s)
	s.GreaterOrEqual(time.Since(start), 200*time.Millisecond)
	s.Less(time.Since(start), waitFor)
}

func (s *ProxySuite) TestTrafficInEitherDirectionKeepsATunnelOpen() {
	up := newRawUpstream(s.T(), nil)
	env := startProxy(s, envCfg{upstream: up.ln.Addr().String(), idle: 400 * time.Millisecond})
	c := openTunnel(s, env, tunnelAuth)
	c.send(s, string(helloRecord(sniExt(tunnelHost))))
	for range 12 {
		time.Sleep(100 * time.Millisecond)
		c.send(s, "x")
	}
	s.Require().NoError(c.conn.(*net.TCPConn).CloseWrite())
	s.Eventually(func() bool { return up.done() == 1 }, waitFor, 10*time.Millisecond)
	s.Len(up.bytes()[0], len(helloRecord(sniExt(tunnelHost)))+12)
}

func (s *ProxySuite) TestAOneWayStreamIsNotIdle() {
	up := newHandledUpstream(s.T(), nil, func(conn net.Conn) {
		_ = conn.SetReadDeadline(time.Now().Add(waitFor))
		_, _ = io.ReadFull(conn, make([]byte, len(helloRecord(sniExt(tunnelHost)))))
		for range 12 {
			time.Sleep(100 * time.Millisecond)
			if _, err := conn.Write([]byte("y")); err != nil {
				return
			}
		}
	})
	env := startProxy(s, envCfg{upstream: up.ln.Addr().String(), idle: 400 * time.Millisecond})
	c := openTunnel(s, env, tunnelAuth)
	c.send(s, string(helloRecord(sniExt(tunnelHost))))
	got, err := io.ReadAll(c.in)
	s.Require().NoError(err)
	s.Equal(strings.Repeat("y", 12), string(got))
}

func (s *ProxySuite) TestATunnelEndsAtItsLifetime() {
	up := newRawUpstream(s.T(), nil)
	env := startProxy(s, envCfg{upstream: up.ln.Addr().String(), lifetime: 500 * time.Millisecond})
	c := openTunnel(s, env, tunnelAuth)
	c.send(s, string(helloRecord(sniExt(tunnelHost))))
	start := time.Now()
	for {
		time.Sleep(50 * time.Millisecond)
		if _, err := io.WriteString(c.conn, "x"); err != nil || time.Since(start) > waitFor {
			break
		}
	}
	elapsed := time.Since(start)
	s.GreaterOrEqual(elapsed, 400*time.Millisecond)
	s.Less(elapsed, waitFor)
	c.closed(s)
}

func (s *ProxySuite) TestOpenConnectionsAreCapped() {
	up := newRawUpstream(s.T(), nil)
	env := startProxy(s, envCfg{upstream: up.ln.Addr().String(), maxConns: 2})
	first := openTunnel(s, env, tunnelAuth)
	openTunnel(s, env, tunnelAuth)

	third := dialProxy(s, env)
	third.send(s, connectLine(tunnelAuth))
	_ = third.conn.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
	_, err := third.in.Peek(1)
	s.Require().ErrorIs(err, os.ErrDeadlineExceeded, "a third connection waits for a slot")

	s.Require().NoError(first.conn.Close())
	_ = third.conn.SetReadDeadline(time.Now().Add(waitFor))
	third.established(s)
}

func (s *ProxySuite) TestStoppingEndsEveryTunnel() {
	up := newRawUpstream(s.T(), nil)
	env := startProxy(s, envCfg{upstream: up.ln.Addr().String()})
	c := openTunnel(s, env, tunnelAuth)
	c.send(s, string(helloRecord(sniExt(tunnelHost))))
	s.Require().NoError(env.stop())
	c.closed(s)
	s.Eventually(func() bool { return up.done() == 1 }, waitFor, 10*time.Millisecond)
	_, err := net.DialTimeout("tcp", env.addr, time.Second)
	s.Error(err, "the listener is closed")
}

func (s *ProxySuite) TestRecorderSeesOnlySafeText() {
	env := startProxy(s, envCfg{})
	long := strings.Repeat("secretlabel", 30) + ".com:443"
	authorities := []string{zonedAuthority, localAuthority, long}
	for _, authority := range authorities {
		doRequest(s, env, connectLine(authority))
	}
	denied, _ := env.rec.snapshot()
	s.Require().Len(denied, 3)
	for i, d := range denied {
		s.Empty(d.Host, authorities[i][:12])
		s.True(d.AddressRedacted, authorities[i][:12])
	}
	s.NotContains(env.logs.String(), "secretlabel")
	s.NotContains(env.logs.String(), "eth0")
	s.NotContains(env.logs.String(), "localhost")
}

func (s *ProxySuite) TestNewProxyChecksItsConfig() {
	dialer := NewDialer(newPolicy(s.T(), nil))
	good := Config{Bind: bindLoopback, SessionID: testSession, Dialer: dialer}
	_, err := NewProxy(good)
	s.Require().NoError(err)

	with := func(mutate func(*Config)) Config {
		cfg := good
		mutate(&cfg)
		return cfg
	}
	for name, cfg := range map[string]Config{
		"no bind":            with(func(c *Config) { c.Bind = "" }),
		"port only":          with(func(c *Config) { c.Bind = ":8080" }),
		"no port":            with(func(c *Config) { c.Bind = loopback4 }),
		"host name":          with(func(c *Config) { c.Bind = "localhost:8080" }),
		"wildcard v4":        with(func(c *Config) { c.Bind = "0.0.0.0:8080" }),
		"wildcard v6":        with(func(c *Config) { c.Bind = "[::]:8080" }),
		"multicast":          with(func(c *Config) { c.Bind = "224.0.0.1:8080" }),
		"zoned":              with(func(c *Config) { c.Bind = "[fe80::1%eth0]:8080" }),
		"mapped":             with(func(c *Config) { c.Bind = "[::ffff:127.0.0.1]:8080" }),
		"no dialer":          with(func(c *Config) { c.Dialer = nil }),
		"no session":         with(func(c *Config) { c.SessionID = "" }),
		"upper case session": with(func(c *Config) { c.SessionID = "P-pool-abc" }),
		"space in session":   with(func(c *Config) { c.SessionID = "p-pool abc" }),
		"newline in session": with(func(c *Config) { c.SessionID = "p-pool\nabc" }),
		"long session":       with(func(c *Config) { c.SessionID = "p" + strings.Repeat("a", 45) }),
		"negative conns":     with(func(c *Config) { c.MaxConns = -1 }),
		"too many conns":     with(func(c *Config) { c.MaxConns = maxConnsLimit + 1 }),
		"negative idle":      with(func(c *Config) { c.IdleTimeout = -time.Second }),
		"negative lifetime":  with(func(c *Config) { c.MaxLifetime = -time.Second }),
	} {
		_, err := NewProxy(cfg)
		s.Error(err, name)
	}
	for _, id := range []string{"p-a-b", "p" + strings.Repeat("a", 44)} {
		_, err := NewProxy(with(func(c *Config) { c.SessionID = id }))
		s.NoError(err, id)
	}
	_, err = NewProxy(with(func(c *Config) { c.MaxConns = maxConnsLimit }))
	s.NoError(err)
}

func (s *ProxySuite) TestDefaultsAreApplied() {
	p, err := NewProxy(
		Config{
			Bind:      bindLoopback,
			SessionID: testSession,
			Dialer:    NewDialer(newPolicy(s.T(), nil)),
		},
	)
	s.Require().NoError(err)
	s.Equal(defaultMaxConns, p.maxConns)
	s.Equal(defaultIdleTimeout, p.idle)
	s.Equal(defaultMaxLifetime, p.lifetime)
	s.Equal(10*time.Second, p.headerTimeout)
	s.Equal(10*time.Second, p.helloTimeout)
	s.NotNil(p.rec)
	s.NotNil(p.log)
}

func (s *ProxySuite) TestListenAndServeOrder() {
	cfg := Config{
		Bind:      bindLoopback,
		SessionID: testSession,
		Dialer:    NewDialer(newPolicy(s.T(), nil)),
	}
	p, err := NewProxy(cfg)
	s.Require().NoError(err)
	s.Error(p.Serve(context.Background()), "serving before listening")
	_, err = p.Listen()
	s.Require().NoError(err)
	_, err = p.Listen()
	s.Error(err, "listening twice")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.NoError(p.Serve(ctx))
	s.Error(p.Serve(context.Background()), "serving twice")
}

func (s *ProxySuite) TestParseConnect() {
	long := strings.Repeat("a", 300) + ".com:443"
	redacted := func(port int) Target { return Target{Port: port, AddressRedacted: true} }
	for _, c := range []struct {
		in     string
		host   string
		port   int
		target Target
		reason Reason
	}{
		{tunnelAuth, tunnelHost, 443, Target{Host: tunnelHost, Port: 443}, ""},
		{"EXAMPLE.com.:8443", tunnelHost, 8443, Target{Host: tunnelHost, Port: 8443}, ""},
		{tunnelHost + ":65535", tunnelHost, 65535, Target{Host: tunnelHost, Port: 65535}, ""},
		{tunnelHost + ":1", tunnelHost, 1, Target{Host: tunnelHost, Port: 1}, ""},
		{publicHostPort, "", 443, Target{Host: publicAddr, Port: 443}, IPLiteral},
		{"[2606:4700::1111]:443", "", 443, Target{Host: "2606:4700::1111", Port: 443}, IPLiteral},
		{zonedAuthority, "", 443, redacted(443), IPLiteral},
		{tunnelHost, "", 0, redacted(0), BadRequest},
		{"", "", 0, redacted(0), BadRequest},
		{":443", "", 443, redacted(443), BadRequest},
		{tunnelHost + ":", tunnelHost, 0, Target{Host: tunnelHost}, BadRequest},
		{tunnelHost + ":0", tunnelHost, 0, Target{Host: tunnelHost}, BadRequest},
		{tunnelHost + ":65536", tunnelHost, 0, Target{Host: tunnelHost}, BadRequest},
		{tunnelHost + ":99999", tunnelHost, 0, Target{Host: tunnelHost}, BadRequest},
		{tunnelHost + ":123456", tunnelHost, 0, Target{Host: tunnelHost}, BadRequest},
		{tunnelHost + ":+443", tunnelHost, 0, Target{Host: tunnelHost}, BadRequest},
		{tunnelHost + ":0443", tunnelHost, 0, Target{Host: tunnelHost}, BadRequest},
		{tunnelHost + ":44a", tunnelHost, 0, Target{Host: tunnelHost}, BadRequest},
		{"user@" + tunnelHost + ":443", "", 443, redacted(443), BadRequest},
		{"[example.com]:443", "", 443, redacted(443), BadRequest},
		{localAuthority, "", 443, redacted(443), BadRequest},
		{long, "", 0, redacted(0), BadRequest},
	} {
		req, reason := parseConnect(c.in)
		s.Equal(c.reason, reason, c.in)
		s.Equal(c.target, req.target, c.in)
		s.Equal(c.host, req.host, c.in)
		s.Equal(c.port, req.port, c.in)
	}
}

func targetsOf(allowed []Allow) []Target {
	out := make([]Target, 0, len(allowed))
	for _, a := range allowed {
		out = append(out, a.Target)
	}
	return out
}

func (s *ProxySuite) TestARefusedAnswerReachesTheRecorderAsThePeerOnly() {
	env := startProxy(s, envCfg{answers: addrs(publicAddr, "172.17.0.9")})
	resp, body := doRequest(s, env, connectLine(tunnelAuth))
	s.Equal(http.StatusForbidden, resp.StatusCode)
	s.Equal(denyBody, body)
	s.NotContains(body, "172.17")
	denied := expectDenials(s, env, PoolNetwork)
	s.Equal("172.17.0.9", denied[0].Peer.String())
	s.Equal(Target{Host: tunnelHost, Port: 443}, denied[0].Target)

	env = startProxy(s, envCfg{})
	_, _ = doRequest(s, env, connectLine("evil.example.org:443"))
	denied = expectDenials(s, env, NotAllowed)
	s.False(denied[0].Peer.IsValid())
}

func (s *ProxySuite) TestAnAllowedTunnelNamesItsRule() {
	server, pool := tlsUpstream(s)
	env := startProxy(s, envCfg{
		upstream: server.Listener.Addr().String(),
		rules:    []poolconfig.AllowRule{host("other.example.net"), host(tunnelHost)},
	})
	c := openTunnel(s, env, tunnelAuth)
	conn := tls.Client(
		c.conn,
		&tls.Config{ServerName: tunnelHost, RootCAs: pool, MinVersion: tls.VersionTLS12},
	)
	s.Require().NoError(conn.Handshake())
	s.Eventually(func() bool {
		_, allowed := env.rec.snapshot()
		return len(allowed) == 1
	}, waitFor, 10*time.Millisecond)
	_, allowed := env.rec.snapshot()
	s.Equal(Target{Host: tunnelHost, Port: 443}, allowed[0].Target)
	s.Equal(1, ruleIndex(allowed[0].Rule))
}

const unavailableBody = "egress audit unavailable for session " + testSession + "\n"

func (s *ProxySuite) TestAnUnhealthyProxyRefusesEveryNewRequestWithAFixed503() {
	var healthy atomic.Bool
	env := startProxy(s, envCfg{healthy: healthy.Load})
	for _, raw := range []string{
		connectLine(tunnelAuth),
		connectLine("evil.example.org:443"),
		connectLine("bad..host:443"),
		"GET / HTTP/1.1\r\nHost: example.com\r\n\r\n",
	} {
		resp, body := doRequest(s, env, raw)
		s.Equal(http.StatusServiceUnavailable, resp.StatusCode, raw)
		s.Equal(unavailableBody, body, raw)
		s.True(resp.Close, raw)
	}
	s.Empty(env.dialed.all(), "no dial while unhealthy")
	denied, allowed := env.rec.snapshot()
	s.Empty(denied, "nothing is recorded while unhealthy")
	s.Empty(allowed)

	healthy.Store(true)
	resp, body := doRequest(s, env, connectLine("evil.example.org:443"))
	s.Equal(http.StatusForbidden, resp.StatusCode)
	s.Equal(denyBody, body)
	expectDenials(s, env, NotAllowed)
}

// countingUpstream accepts connections and counts every byte they send. The
// returned func waits for the first connection to end and returns the count.
func countingUpstream(s *ProxySuite) (string, func() int) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = ln.Close() })
	counted := make(chan int, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		n, _ := io.Copy(io.Discard, conn)
		counted <- int(n)
	}()
	return ln.Addr().String(), func() int {
		select {
		case n := <-counted:
			return n
		case <-time.After(5 * time.Second):
			s.FailNow("the upstream connection never ended")
			return -1
		}
	}
}

func (s *ProxySuite) TestATunnelWhoseAllowCannotBeRecordedCarriesNoBytes() {
	addr, upstreamBytes := countingUpstream(s)
	env := startProxy(s, envCfg{upstream: addr, refuseAllow: true})
	c := openTunnel(s, env, tunnelAuth)
	conn := tls.Client(
		c.conn,
		&tls.Config{
			ServerName: tunnelHost, RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12,
		},
	)
	s.Error(conn.Handshake(), "no upstream byte reaches a client whose allow went unrecorded")
	c.closed(s)
	s.Equal(0, upstreamBytes(), "not even the ClientHello reaches the upstream")
	_, allowed := env.rec.snapshot()
	s.Len(allowed, 1, "the allow was offered to the recorder before any forward")
}

func (s *ProxySuite) TestATunnelOpenBeforeTheProxyTurnsUnhealthyKeepsRunning() {
	server, pool := tlsUpstream(s)
	var healthy atomic.Bool
	healthy.Store(true)
	env := startProxy(s, envCfg{
		upstream: server.Listener.Addr().String(),
		healthy:  healthy.Load,
	})
	c := openTunnel(s, env, tunnelAuth)
	conn := tls.Client(
		c.conn,
		&tls.Config{ServerName: tunnelHost, RootCAs: pool, MinVersion: tls.VersionTLS12},
	)
	s.Require().NoError(conn.Handshake())
	healthy.Store(false)
	_, err := io.WriteString(
		conn, "GET / HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n")
	s.Require().NoError(err)
	body, _ := io.ReadAll(conn)
	s.Contains(string(body), "hello from upstream")
}
