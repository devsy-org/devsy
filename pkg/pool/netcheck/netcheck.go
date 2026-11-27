// Package netcheck classifies the addresses an egress rule or the egress
// proxy may reach. Config validation and the proxy share these tables so they
// cannot disagree.
package netcheck

import (
	"net/netip"
	"slices"
)

// The tables follow the IANA IPv4 and IPv6 special-purpose address registries
// (both last modified 2026-10-13, checked 2026-11-27). Every entry marked
// Globally Reachable False or N/A is blocked: in neverAllowed, or in private
// when a cidr rule may allow it. Entries marked True stay open, except three
// that sit inside a blocked parent or are transition forms denied whole:
// 192.0.0.9/32 and 192.0.0.10/32 inside 192.0.0.0/24, and 64:ff9b::/96.
//
// 2001::/23 is the IETF protocol assignments block and is marked False, so
// all of it is blocked except the globally reachable children the registry
// lists (ietfOpen). Space nobody has assigned yet, and the deprecated ORCHID
// block 2001:10::/28, stay blocked. Teredo 2001::/32 is a child marked N/A
// and stays blocked.
//
// neverAllowed holds addresses no allow rule can reach: loopback, link-local,
// cloud metadata, multicast, reserved space, and every IPv6 form that carries
// an IPv4 address through a gateway or relay (NAT64, 6to4, Teredo, SIIT and
// IPv4-compatible). Those forms are denied whole rather than unwrapped,
// because their traffic ends at the gateway, not at the embedded IPv4 address.
// IPv4-mapped IPv6 is the one exception: it is a literal IPv4 address, so
// the checks unmap it first and the ::ffff:0:0/96 entry only serves prefix
// checks.
var neverAllowed = mustPrefixes(
	"0.0.0.0/8",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"100.100.100.200/32",
	"168.63.129.16/32",
	"192.0.0.192/32",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"::/96",
	"::ffff:0:0/96",
	"::ffff:0:0:0/96",
	"64:ff9b::/96",
	"64:ff9b:1::/48",
	"2001::/32",
	"2002::/16",
	"ff00::/8",
	"fe80::/10",
	"fd00:ec2::/32",
	"fd20:ce::254/128",
	"192.0.0.0/24",
	"192.88.99.0/24",
	"fec0::/10",
	"100::/64",
	"100:0:0:1::/64",
	"2001:2::/48",
	"5f00::/16",
	ietfProtocol,
)

const ietfProtocol = "2001::/23"

var ietfPrefix = netip.MustParsePrefix(ietfProtocol)

// ietfOpen holds the children of 2001::/23 the registry marks globally
// reachable: PCP, TURN and SRP anycast, AMT, AS112, ORCHIDv2 and the drone ID
// tags.
var ietfOpen = mustPrefixes(
	"2001:1::1/128",
	"2001:1::2/128",
	"2001:1::3/128",
	"2001:3::/32",
	"2001:4:112::/48",
	"2001:20::/28",
	"2001:30::/28",
)

// private holds addresses an allow rule may reach only through a cidr rule
// that covers them: RFC 1918, carrier-grade NAT, unique local, documentation
// and benchmark ranges.
var private = mustPrefixes(
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"100.64.0.0/10",
	"192.0.2.0/24",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"198.18.0.0/15",
	"fc00::/7",
	"2001:db8::/32",
	"3fff::/20",
)

func mustPrefixes(values ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(values))
	for i, v := range values {
		out[i] = netip.MustParsePrefix(v)
	}
	return out
}

// NeverAllowed reports whether addr is unreachable whatever the policy says.
// It fails closed: an invalid or zoned address is never allowed. An
// IPv4-mapped address is judged as the IPv4 address it carries, so dial the
// unmapped form.
func NeverAllowed(addr netip.Addr) bool {
	addr, ok := normalize(addr)
	return !ok || blocked(addr)
}

func blocked(addr netip.Addr) bool {
	for _, r := range neverAllowed {
		if r.Contains(addr) && !openChild(r, addr) {
			return true
		}
	}
	return false
}

// Private reports whether addr is in a private range that needs a covering
// cidr rule. It fails closed: an invalid or zoned address counts as private,
// so callers check NeverAllowed first and never allow on Private alone.
func Private(addr netip.Addr) bool {
	addr, ok := normalize(addr)
	return !ok || contains(private, addr)
}

// OverlapsNeverAllowed returns the first never-allowed range that prefix
// overlaps. An invalid prefix reports an empty range and true.
func OverlapsNeverAllowed(prefix netip.Prefix) (netip.Prefix, bool) {
	if !prefix.IsValid() {
		return netip.Prefix{}, true
	}
	for _, r := range neverAllowed {
		if prefix.Overlaps(r) && !openPrefix(r, prefix) {
			return r, true
		}
	}
	return netip.Prefix{}, false
}

// openChild reports whether addr is in an open child of blocked range r.
func openChild(r netip.Prefix, addr netip.Addr) bool {
	return r == ietfPrefix && contains(ietfOpen, addr)
}

// openPrefix reports whether prefix lies wholly inside one open child of
// blocked range r. A prefix wider than every child holds blocked space too.
func openPrefix(r, prefix netip.Prefix) bool {
	return r == ietfPrefix && slices.ContainsFunc(ietfOpen, func(o netip.Prefix) bool {
		return o.Bits() <= prefix.Bits() && o.Contains(prefix.Addr())
	})
}

func normalize(addr netip.Addr) (netip.Addr, bool) {
	if !addr.IsValid() || addr.Zone() != "" {
		return addr, false
	}
	return addr.Unmap(), true
}

func contains(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
