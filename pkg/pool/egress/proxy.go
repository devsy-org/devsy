package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
)

const (
	defaultMaxConns    = 128
	maxConnsLimit      = 4096
	defaultIdleTimeout = 5 * time.Minute
	defaultMaxLifetime = time.Hour
	headerTimeout      = 10 * time.Second
	helloTimeout       = 10 * time.Second
	maxHeaderBytes     = 8 << 10
	// maxAuthorityLen is a 253 byte name, a colon and a five digit port.
	maxAuthorityLen    = 253 + 1 + 5
	connectEstablished = "HTTP/1.1 200 Connection Established\r\n\r\n"
)

var sessionIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,44}$`)

// Target names a destination in the form an audit event may carry. It never
// holds client text that is not a valid host name or IP literal.
type Target struct {
	// Host is the normalized name or the IP literal. It is empty when
	// AddressRedacted is set.
	Host string
	// Port is 0 when the request had no valid port.
	Port            int
	AddressRedacted bool
}

// Denial is one refused connection.
type Denial struct {
	Target
	Reason Reason
	// Peer is the resolved or socket address that caused the refusal, with no
	// zone and no IPv4-mapped form. It is the zero value when the refusal did
	// not concern one address.
	Peer netip.Addr
}

// Allow is one tunnel that passed every check.
type Allow struct {
	Target
	// Rule is the allow rule that permitted the target.
	Rule Rule
}

// Recorder receives the proxy's decisions. Calls come from many goroutines.
type Recorder interface {
	// Denied is called once for every refused request, before the client
	// sees the refusal or the connection closes.
	Denied(Denial)
	// Allowed is called once for every tunnel that passed every check.
	Allowed(Allow)
}

type discardRecorder struct{}

func (discardRecorder) Denied(Denial) {}
func (discardRecorder) Allowed(Allow) {}

// Config holds what one proxy needs. Zero limits mean the defaults.
type Config struct {
	// Bind is the ip:port to listen on. It must name one interface address:
	// the sidecar also sits on a network the agent must not reach, so a
	// wildcard bind is refused. Port 0 picks a free port.
	Bind string
	// SessionID is named in the fixed refusal bodies.
	SessionID string
	// Dialer opens every upstream connection and applies the policy.
	Dialer *Dialer
	// Recorder, Logger are optional. Detail that must not reach the agent,
	// such as a resolved address in a connect error, goes only to Logger.
	Recorder Recorder
	Logger   *slog.Logger
	// MaxConns caps open client connections, tunnels included.
	MaxConns int
	// IdleTimeout closes a tunnel with no bytes in either direction.
	IdleTimeout time.Duration
	// MaxLifetime closes a tunnel however busy it is.
	MaxLifetime time.Duration
}

// Proxy is an HTTP CONNECT proxy that tunnels TLS only. It checks the name
// before any lookup, dials through the Dialer, and then requires the
// ClientHello to name the CONNECT host and no other.
//
// Only the server name is checked. The proxy cannot see the inner Host
// header, so on a shared-IP or CDN host the resolved-address checks are the
// only limit. A ClientHello with encrypted client hello, including GREASE, is
// refused.
type Proxy struct {
	bind          netip.AddrPort
	session       string
	dialer        *Dialer
	rec           Recorder
	log           *slog.Logger
	maxConns      int
	idle          time.Duration
	lifetime      time.Duration
	headerTimeout time.Duration
	helloTimeout  time.Duration

	mu      sync.Mutex
	ln      net.Listener
	served  bool
	closing bool
	active  sync.WaitGroup
}

// NewProxy checks the config and returns a proxy that is not yet listening.
func NewProxy(cfg Config) (*Proxy, error) {
	cfg = cfg.withDefaults()
	bind, err := cfg.validate()
	if err != nil {
		return nil, err
	}
	return &Proxy{
		bind:          bind,
		session:       cfg.SessionID,
		dialer:        cfg.Dialer,
		rec:           cfg.Recorder,
		log:           cfg.Logger,
		maxConns:      cfg.MaxConns,
		idle:          cfg.IdleTimeout,
		lifetime:      cfg.MaxLifetime,
		headerTimeout: headerTimeout,
		helloTimeout:  helloTimeout,
	}, nil
}

// Listen binds the configured address and returns the address it got.
func (p *Proxy) Listen() (net.Addr, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ln != nil {
		return nil, errors.New("egress proxy is already listening")
	}
	ln, err := net.Listen("tcp", p.bind.String())
	if err != nil {
		return nil, fmt.Errorf("egress proxy listen: %w", err)
	}
	p.ln = newLimitListener(ln, p.maxConns)
	return ln.Addr(), nil
}

// Serve accepts connections until ctx is done, then closes the listener and
// every tunnel and waits for the handlers to return. It returns nil after a
// requested stop. Listen must have been called.
func (p *Proxy) Serve(ctx context.Context) error {
	ln, err := p.takeListener()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	srv := p.server(ctx)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	var serveErr error
	select {
	case serveErr = <-done:
	case <-ctx.Done():
		_ = srv.Close()
		serveErr = <-done
	}
	cancel()
	p.drain()
	if errors.Is(serveErr, http.ErrServerClosed) {
		return nil
	}
	return serveErr
}

func (c Config) withDefaults() Config {
	if c.MaxConns == 0 {
		c.MaxConns = defaultMaxConns
	}
	if c.IdleTimeout == 0 {
		c.IdleTimeout = defaultIdleTimeout
	}
	if c.MaxLifetime == 0 {
		c.MaxLifetime = defaultMaxLifetime
	}
	if c.Recorder == nil {
		c.Recorder = discardRecorder{}
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
	return c
}

func (c Config) validate() (netip.AddrPort, error) {
	bind, err := parseBind(c.Bind)
	if err != nil {
		return netip.AddrPort{}, err
	}
	switch {
	case c.Dialer == nil:
		return bind, errors.New("egress proxy needs a dialer")
	case !sessionIDPattern.MatchString(c.SessionID):
		return bind, errors.New("egress proxy session ID is invalid")
	case c.MaxConns < 1 || c.MaxConns > maxConnsLimit:
		return bind, fmt.Errorf("egress proxy max connections must be 1 to %d", maxConnsLimit)
	case c.IdleTimeout < 0 || c.MaxLifetime < 0:
		return bind, errors.New("egress proxy timeouts must not be negative")
	}
	return bind, nil
}

func parseBind(value string) (netip.AddrPort, error) {
	bind, err := netip.ParseAddrPort(value)
	if err != nil {
		return netip.AddrPort{}, errors.New("egress proxy bind must be an ip:port")
	}
	addr := bind.Addr()
	if addr.IsUnspecified() || addr.IsMulticast() || addr.Zone() != "" || addr.Is4In6() {
		return netip.AddrPort{}, errors.New("egress proxy bind must name one interface address")
	}
	return bind, nil
}

func (p *Proxy) takeListener() (net.Listener, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ln == nil {
		return nil, errors.New("egress proxy is not listening")
	}
	if p.served {
		return nil, errors.New("egress proxy already served")
	}
	p.served = true
	return p.ln, nil
}

func (p *Proxy) server(ctx context.Context) *http.Server {
	return &http.Server{
		Handler:           http.HandlerFunc(p.handle),
		ReadHeaderTimeout: p.headerTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          log.New(io.Discard, "", 0),
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
}

func (p *Proxy) enter() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closing {
		return false
	}
	p.active.Add(1)
	return true
}

func (p *Proxy) drain() {
	p.mu.Lock()
	p.closing = true
	p.mu.Unlock()
	p.active.Wait()
}

func (p *Proxy) handle(w http.ResponseWriter, r *http.Request) {
	if !p.enter() {
		p.reply(w, http.StatusServiceUnavailable, "egress proxy is stopping\n")
		return
	}
	defer p.active.Done()
	if r.Method != http.MethodConnect {
		w.Header().Set("Allow", http.MethodConnect)
		p.reply(w, http.StatusMethodNotAllowed, "egress proxy accepts CONNECT only\n")
		return
	}
	req, reason := parseConnect(r.RequestURI)
	if reason != "" {
		p.deny(w, Denial{Target: req.target, Reason: reason})
		return
	}
	p.connect(w, r, req)
}

// connect dials before it answers, so a refusal or a failed connect can still
// be a plain HTTP status. The ClientHello is checked after the 200.
func (p *Proxy) connect(w http.ResponseWriter, r *http.Request, req connectRequest) {
	opened, err := p.dialer.Open(r.Context(), req.host, req.port)
	if err != nil {
		p.dialFailed(w, req, err)
		return
	}
	req.rule = opened.Rule
	upstream := opened.Conn
	client, in, err := hijack(w)
	if err != nil {
		_ = upstream.Close()
		p.log.Warn("egress hijack failed", "error", err.Error())
		return
	}
	p.tunnel(r.Context(), tunnelConns{client: client, clientIn: in, up: upstream}, req)
}

func (p *Proxy) dialFailed(w http.ResponseWriter, req connectRequest, err error) {
	if refused, ok := errors.AsType[*DeniedError](err); ok {
		p.deny(w, Denial{Target: req.target, Reason: refused.Reason, Peer: refused.Peer})
		return
	}
	p.log.Warn("egress upstream connect failed",
		"host", req.target.Host, "port", req.port, "error", err.Error())
	p.reply(w, http.StatusBadGateway, "egress upstream unavailable for session "+p.session+"\n")
}

func (p *Proxy) deny(w http.ResponseWriter, d Denial) {
	p.recordDenied(d)
	p.reply(w, http.StatusForbidden, "egress denied for session "+p.session+"\n")
}

func (p *Proxy) recordDenied(d Denial) {
	p.rec.Denied(d)
	p.log.Debug("egress denied",
		"reason", string(d.Reason), "host", d.Host, "port", d.Port, "peer", d.Peer.String())
}

// reply writes a fixed body. It never echoes client text.
func (p *Proxy) reply(w http.ResponseWriter, status int, body string) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Content-Length", fmt.Sprint(len(body)))
	h.Set("Connection", "close")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// connectRequest is a CONNECT target that passed the syntax checks.
type connectRequest struct {
	host   string
	port   int
	target Target
	// rule is set once the dialer has allowed the target.
	rule Rule
}

// parseConnect reads the raw request target of a CONNECT. A refusal still
// returns the part of the target that is safe to record.
func parseConnect(authority string) (connectRequest, Reason) {
	redacted := connectRequest{target: Target{AddressRedacted: true}}
	if len(authority) > maxAuthorityLen {
		return redacted, BadRequest
	}
	host, portText, err := net.SplitHostPort(authority)
	if err != nil {
		return redacted, BadRequest
	}
	port := parsePort(portText)
	req := connectRequest{port: port, target: Target{Port: port}}
	if addr, err := netip.ParseAddr(host); err == nil {
		req.target.Host, req.target.AddressRedacted = literalText(addr)
		return req, IPLiteral
	}
	name, ok := poolconfig.NormalizeHost(host)
	if !ok || authority[0] == '[' {
		req.target.AddressRedacted = true
		return req, BadRequest
	}
	req.host, req.target.Host = name, name
	if port == 0 {
		return req, BadRequest
	}
	return req, ""
}

// parsePort returns 0 unless text is 1 to 65535 in plain digits.
func parsePort(text string) int {
	if len(text) == 0 || len(text) > 5 || text[0] == '0' {
		return 0
	}
	port := 0
	for _, c := range text {
		if c < '0' || c > '9' {
			return 0
		}
		port = port*10 + int(c-'0')
	}
	if port > 65535 {
		return 0
	}
	return port
}

// literalText returns the text to record for an IP literal. A zone is client
// text, so it is dropped with the address.
func literalText(addr netip.Addr) (string, bool) {
	if addr.Zone() != "" {
		return "", true
	}
	return addr.String(), false
}
