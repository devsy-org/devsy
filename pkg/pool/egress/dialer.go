package egress

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"syscall"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
)

const (
	resolveTimeout = 5 * time.Second
	dialTimeout    = 10 * time.Second
)

// Resolver is the part of net.Resolver the dialer uses.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// DeniedError means the policy refused the target. Any other error from Dial
// is a failed connect to a target the policy allowed.
type DeniedError struct {
	Reason Reason
}

func (e *DeniedError) Error() string { return "egress denied: " + string(e.Reason) }

func denied(d Decision) error { return &DeniedError{Reason: d.Reason} }

// connectFunc opens one TCP connection to target. It must call verify with
// the address of the socket it is about to connect, before connecting, and
// stop on an error.
type connectFunc func(ctx context.Context, target netip.AddrPort, verify func(address string) error) (net.Conn, error)

// Dialer opens connections to targets a Policy allows. A name is matched
// before any lookup, resolved once, and every answer is checked. Only then
// does it connect, to one checked address at a time, in the form
// addr.Unmap(). The connect step checks the socket address again, so no
// second lookup or rebinding can change the target.
type Dialer struct {
	policy   *Policy
	resolver Resolver
	connect  connectFunc
}

// NewDialer returns a Dialer that resolves with the pure-Go resolver.
func NewDialer(p *Policy) *Dialer {
	return &Dialer{policy: p, resolver: &net.Resolver{PreferGo: true}, connect: connectTCP}
}

// Dial connects to host and port. host is a bare name or IP address, with no
// brackets or port. A policy refusal returns a *DeniedError.
func (d *Dialer) Dial(ctx context.Context, host string, port int) (net.Conn, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return d.dialLiteral(ctx, addr, port)
	}
	return d.dialName(ctx, host, port)
}

func (d *Dialer) dialLiteral(ctx context.Context, addr netip.Addr, port int) (net.Conn, error) {
	if dec := d.policy.CheckAddr(addr, port); !dec.Allow {
		return nil, denied(dec)
	}
	return d.dialChecked(ctx, []netip.Addr{addr}, port, func(a netip.Addr) Decision {
		return d.policy.CheckAddr(a, port)
	})
}

func (d *Dialer) dialName(ctx context.Context, host string, port int) (net.Conn, error) {
	if dec := d.policy.CheckName(host, port); !dec.Allow {
		return nil, denied(dec)
	}
	addrs, err := d.lookup(ctx, host)
	if err != nil {
		return nil, &DeniedError{Reason: ResolveFailed}
	}
	if dec := d.policy.CheckResolved(host, port, addrs); !dec.Allow {
		return nil, denied(dec)
	}
	return d.dialChecked(ctx, addrs, port, func(a netip.Addr) Decision {
		return d.policy.CheckResolved(host, port, []netip.Addr{a})
	})
}

// lookup asks for the fully qualified name, so no search domain applies.
func (d *Dialer) lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	name, ok := poolconfig.NormalizeHost(host)
	if !ok {
		return nil, errors.New("invalid host")
	}
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	return d.resolver.LookupNetIP(ctx, "ip", name+".")
}

func (d *Dialer) dialChecked(
	ctx context.Context, addrs []netip.Addr, port int, check func(netip.Addr) Decision,
) (net.Conn, error) {
	if port < 1 || port > math.MaxUint16 {
		return nil, &DeniedError{Reason: BadRequest}
	}
	var last error
	for _, addr := range addrs {
		target := netip.AddrPortFrom(addr.Unmap(), uint16(port))
		conn, err := d.connectOne(ctx, target, check)
		if err == nil {
			return conn, nil
		}
		if refused, ok := errors.AsType[*DeniedError](err); ok {
			return nil, refused
		}
		if last = err; ctx.Err() != nil {
			break
		}
	}
	return nil, fmt.Errorf("egress connect: %w", last)
}

func (d *Dialer) connectOne(
	ctx context.Context, target netip.AddrPort, check func(netip.Addr) Decision,
) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	return d.connect(ctx, target, func(address string) error {
		return recheck(target, address, check)
	})
}

// recheck runs on the socket address just before connect. It must still pass
// the policy and be exactly the address that was checked.
func recheck(target netip.AddrPort, address string, check func(netip.Addr) Decision) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return &DeniedError{Reason: BadRequest}
	}
	if dec := check(ap.Addr()); !dec.Allow {
		return denied(dec)
	}
	if ap.Addr().Unmap() != target.Addr() || ap.Port() != target.Port() {
		return &DeniedError{Reason: NotAllowed}
	}
	return nil
}

func connectTCP(
	ctx context.Context,
	target netip.AddrPort,
	verify func(string) error,
) (net.Conn, error) {
	dialer := net.Dialer{
		FallbackDelay: -1,
		Control: func(_, address string, _ syscall.RawConn) error {
			return verify(address)
		},
	}
	return dialer.DialContext(ctx, "tcp", target.String())
}
