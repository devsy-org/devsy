// Package egress decides whether the egress proxy may reach a destination.
// Policy holds no sockets and does no DNS: the caller resolves once and
// passes the answers back for a second check. Dialer does that for a TCP
// connect.
package egress

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"

	"github.com/devsy-org/devsy/pkg/pool/netcheck"
	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
)

// Reason is why a request was denied. The values are the audit
// devsy.egress.reason enum. An allowed request has no reason.
type Reason string

const (
	NotAllowed    Reason = "not_allowed"
	ReservedRange Reason = "reserved_range"
	PrivateRange  Reason = "private_range"
	PoolNetwork   Reason = "pool_network"
	IPLiteral     Reason = "ip_literal"
	ResolveFailed Reason = "resolve_failed"
	BadRequest    Reason = "bad_request"
)

// Decision is the verdict for one request.
type Decision struct {
	Allow  bool
	Reason Reason
}

var allowed = Decision{Allow: true}

func deny(r Reason) Decision { return Decision{Reason: r} }

const (
	defaultPort = 443
	maxPort     = 65535
	// maxAnswers bounds the work for one request. The resolver caps answers
	// first, so a larger set is a caller bug and is refused.
	maxAnswers = 64
)

type hostRule struct {
	pattern string
	ports   []int
}

type cidrRule struct {
	prefix netip.Prefix
	ports  []int
}

// Policy is an immutable allow list. Everything not allowed is denied.
type Policy struct {
	hosts []hostRule
	cidrs []cidrRule
	deny  []netip.Prefix
}

// New compiles the allow rules. alwaysDeny holds the networks the proxy must
// never reach whatever the rules say, such as its own interfaces, the pool
// networks and the cluster CIDRs. A malformed rule is an error, so a policy
// that loads has no rule the matcher silently skips. A cidr rule that
// overlaps a reserved range still loads and still never matches, so the
// runtime does not depend on config validation having run.
func New(cfg poolconfig.Egress, alwaysDeny []netip.Prefix) (*Policy, error) {
	if cfg.Default != "" && cfg.Default != poolconfig.EgressDeny {
		return nil, errors.New("egress default must be deny")
	}
	p := &Policy{}
	for i, rule := range cfg.Allow {
		if err := p.add(rule); err != nil {
			return nil, fmt.Errorf("egress allow rule %d: %w", i, err)
		}
	}
	deny, err := compileDeny(alwaysDeny)
	if err != nil {
		return nil, err
	}
	p.deny = deny
	return p, nil
}

func compileDeny(networks []netip.Prefix) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(networks))
	for i, prefix := range networks {
		if !prefix.IsValid() || prefix.Addr().Is4In6() || prefix.Addr().Zone() != "" {
			return nil, fmt.Errorf("always-deny network %d is invalid", i)
		}
		out = append(out, prefix.Masked())
	}
	return out, nil
}

// CheckName decides a name target before any DNS lookup, so a denied name
// causes no resolver traffic. An allowed name must still pass CheckResolved.
// An IP address in text form is never a name: use CheckAddr.
func (p *Policy) CheckName(host string, port int) Decision {
	if !validPort(port) {
		return deny(BadRequest)
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return deny(IPLiteral)
	}
	name, ok := poolconfig.NormalizeHost(host)
	if !ok {
		return deny(BadRequest)
	}
	if !p.hostAllows(name, port) {
		return deny(NotAllowed)
	}
	return allowed
}

// CheckResolved decides a name target again with the addresses the caller
// resolved. Every address must pass, so one forbidden answer denies the whole
// request. A private address also needs a cidr rule that covers it and the
// port. The caller must dial only these addresses and not resolve again.
func (p *Policy) CheckResolved(host string, port int, addrs []netip.Addr) Decision {
	if d := p.CheckName(host, port); !d.Allow {
		return d
	}
	if len(addrs) == 0 || len(addrs) > maxAnswers {
		return deny(ResolveFailed)
	}
	for _, addr := range addrs {
		if d := p.checkAnswer(addr, port); !d.Allow {
			return d
		}
	}
	return allowed
}

// CheckAddr decides an IP literal target. A literal needs a cidr rule that
// covers it and the port, whatever host rules exist.
func (p *Policy) CheckAddr(addr netip.Addr, port int) Decision {
	if !validPort(port) {
		return deny(BadRequest)
	}
	addr, d := p.screen(addr)
	if !d.Allow {
		return d
	}
	if !p.cidrAllows(addr, port) {
		return deny(IPLiteral)
	}
	return allowed
}

func (p *Policy) add(rule poolconfig.AllowRule) error {
	if (rule.Host == "") == (rule.CIDR == "") {
		return errors.New("must set exactly one of host or cidr")
	}
	ports, err := rulePorts(rule.Ports)
	if err != nil {
		return err
	}
	if rule.Host != "" {
		if !poolconfig.ValidHostPattern(rule.Host) {
			return errors.New("host is not a valid pattern")
		}
		p.hosts = append(p.hosts, hostRule{pattern: rule.Host, ports: ports})
		return nil
	}
	prefix, err := netip.ParsePrefix(rule.CIDR)
	if err != nil || prefix.Addr().Is4In6() {
		return errors.New("cidr is not a valid IPv4 or IPv6 range")
	}
	if poolconfig.PrefixTooWide(prefix) {
		return errors.New("cidr is wider than /8 for IPv4 or /32 for IPv6")
	}
	p.cidrs = append(p.cidrs, cidrRule{prefix: prefix.Masked(), ports: ports})
	return nil
}

func (p *Policy) hostAllows(name string, port int) bool {
	return slices.ContainsFunc(p.hosts, func(r hostRule) bool {
		return slices.Contains(r.ports, port) && poolconfig.HostMatches(r.pattern, name)
	})
}

func (p *Policy) checkAnswer(addr netip.Addr, port int) Decision {
	addr, d := p.screen(addr)
	if !d.Allow {
		return d
	}
	if netcheck.Private(addr) && !p.cidrAllows(addr, port) {
		return deny(PrivateRange)
	}
	return allowed
}

// screen applies the checks no rule can override. It returns the address in
// the form to dial: an IPv4-mapped address unmapped. An invalid or zoned
// address is reserved.
func (p *Policy) screen(addr netip.Addr) (netip.Addr, Decision) {
	if !addr.IsValid() || addr.Zone() != "" {
		return addr, deny(ReservedRange)
	}
	addr = addr.Unmap()
	switch {
	case netcheck.NeverAllowed(addr):
		return addr, deny(ReservedRange)
	case slices.ContainsFunc(p.deny, func(n netip.Prefix) bool { return n.Contains(addr) }):
		return addr, deny(PoolNetwork)
	}
	return addr, allowed
}

func (p *Policy) cidrAllows(addr netip.Addr, port int) bool {
	return slices.ContainsFunc(p.cidrs, func(r cidrRule) bool {
		return r.prefix.Contains(addr) && slices.Contains(r.ports, port)
	})
}

func rulePorts(ports []int) ([]int, error) {
	if len(ports) == 0 {
		return []int{defaultPort}, nil
	}
	for _, port := range ports {
		if !validPort(port) {
			return nil, errors.New("port is outside 1 to 65535")
		}
	}
	return slices.Clone(ports), nil
}

func validPort(port int) bool { return port >= 1 && port <= maxPort }
