package netcheck

import (
	"net/netip"
	"testing"
)

type tier string

const (
	tierNever   tier = "never"
	tierPrivate tier = "private"
	tierOpen    tier = "open"
)

// registryEntry is one row of the IANA special-purpose registries as read on
// 2026-11-27 (both files last modified 2026-10-13). Globally Reachable False
// or N/A rows must be blocked. True rows must stay open, except the rows whose
// want differs from that rule, which the note explains.
type registryEntry struct {
	block string
	name  string
	want  tier
	note  string
}

const (
	thisNetwork = "0.0.0.0/8"
	linkLocal4  = "169.254.0.0/16"
	private10   = "10.0.0.0/8"
	dummy6      = "100:0:0:1::/64"
	bench6      = "2001:2::/48"
	teredo6     = "2001::/32"
	namePrivate = "Private-Use"

	noteParent    = "inside a blocked parent block"
	noteTransform = "transition form denied whole"
)

var registry = []registryEntry{
	{thisNetwork, "This network", tierNever, ""},
	{"0.0.0.0/32", "This host on this network", tierNever, ""},
	{private10, namePrivate, tierPrivate, ""},
	{"100.64.0.0/10", "Shared Address Space", tierPrivate, ""},
	{"127.0.0.0/8", "Loopback", tierNever, ""},
	{linkLocal4, "Link Local", tierNever, ""},
	{"172.16.0.0/12", namePrivate, tierPrivate, ""},
	{"192.0.0.0/24", "IETF Protocol Assignments", tierNever, ""},
	{"192.0.0.0/29", "IPv4 Service Continuity Prefix", tierNever, ""},
	{"192.0.0.8/32", "IPv4 dummy address", tierNever, ""},
	{"192.0.0.9/32", "Port Control Protocol Anycast", tierNever, noteParent},
	{"192.0.0.10/32", "TURN Anycast", tierNever, noteParent},
	{"192.0.0.170/32", "NAT64/DNS64 Discovery", tierNever, ""},
	{"192.0.0.171/32", "NAT64/DNS64 Discovery", tierNever, ""},
	{"192.0.2.0/24", "Documentation (TEST-NET-1)", tierPrivate, ""},
	{"192.31.196.0/24", "AS112-v4", tierOpen, ""},
	{"192.52.193.0/24", "AMT", tierOpen, ""},
	{"192.88.99.0/24", "Deprecated (6to4 Relay Anycast)", tierNever, ""},
	{"192.88.99.2/32", "6a44-relay anycast address", tierNever, ""},
	{"192.168.0.0/16", namePrivate, tierPrivate, ""},
	{"192.175.48.0/24", "Direct Delegation AS112 Service", tierOpen, ""},
	{"198.18.0.0/15", "Benchmarking", tierPrivate, ""},
	{"198.51.100.0/24", "Documentation (TEST-NET-2)", tierPrivate, ""},
	{"203.0.113.0/24", "Documentation (TEST-NET-3)", tierPrivate, ""},
	{"240.0.0.0/4", "Reserved", tierNever, ""},
	{"255.255.255.255/32", "Limited Broadcast", tierNever, ""},
	{"::1/128", "Loopback Address", tierNever, ""},
	{"::/128", "Unspecified Address", tierNever, ""},
	{"::ffff:0:0/96", "IPv4-mapped Address", tierNever, ""},
	{"64:ff9b::/96", "IPv4-IPv6 Translat.", tierNever, noteTransform},
	{"64:ff9b:1::/48", "IPv4-IPv6 Translat.", tierNever, ""},
	{"100::/64", "Discard-Only Address Block", tierNever, ""},
	{dummy6, "Dummy IPv6 Prefix", tierNever, ""},
	{"2001::/23", "IETF Protocol Assignments", tierNever, ""},
	{teredo6, "TEREDO", tierNever, ""},
	{"2001:1::1/128", "Port Control Protocol Anycast", tierOpen, ""},
	{"2001:1::2/128", "TURN Anycast", tierOpen, ""},
	{"2001:1::3/128", "DNS-SD Service Registration Protocol Anycast", tierOpen, ""},
	{bench6, "Benchmarking", tierNever, ""},
	{"2001:3::/32", "AMT", tierOpen, ""},
	{"2001:4:112::/48", "AS112-v6", tierOpen, ""},
	{"2001:10::/28", "Deprecated (previously ORCHID)", tierNever, ""},
	{"2001:20::/28", "ORCHIDv2", tierOpen, ""},
	{"2001:30::/28", "Drone Remote ID Protocol Entity Tags", tierOpen, ""},
	{"2001:db8::/32", "Documentation", tierPrivate, ""},
	{"2002::/16", "6to4", tierNever, ""},
	{"2620:4f:8000::/48", "Direct Delegation AS112 Service", tierOpen, ""},
	{"3fff::/20", "Documentation", tierPrivate, ""},
	{"5f00::/16", "Segment Routing (SRv6) SIDs", tierNever, ""},
	{"fc00::/7", "Unique-Local", tierPrivate, ""},
	{"fe80::/10", "Link-Local Unicast", tierNever, ""},
}

func classify(addr netip.Addr) tier {
	switch {
	case NeverAllowed(addr):
		return tierNever
	case Private(addr):
		return tierPrivate
	default:
		return tierOpen
	}
}

func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().AsSlice()
	for bit := p.Bits(); bit < len(b)*8; bit++ {
		b[bit/8] |= 0x80 >> (bit % 8)
	}
	addr, _ := netip.AddrFromSlice(b)
	return addr
}

func TestSpecialPurposeRegistryEntries(t *testing.T) {
	for _, e := range registry {
		p := netip.MustParsePrefix(e.block)
		for _, addr := range []netip.Addr{p.Masked().Addr(), lastAddr(p)} {
			if got := classify(addr); got != e.want {
				t.Errorf("%s (%s) at %s is %s, want %s", e.block, e.name, addr, got, e.want)
			}
		}
		// A private block can hold never-allowed metadata addresses, so only the
		// other tiers have a fixed answer.
		if _, overlaps := OverlapsNeverAllowed(p); e.want != tierPrivate &&
			overlaps != (e.want == tierNever) {
			t.Errorf("OverlapsNeverAllowed(%s) = %v for a %s entry", e.block, overlaps, e.want)
		}
	}
}

func TestLastAddr(t *testing.T) {
	for in, want := range map[string]string{
		private10:      "10.255.255.255",
		"1.2.3.4/32":   "1.2.3.4",
		"fc00::/7":     "fdff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
		bench6:         "2001:2:0:ffff:ffff:ffff:ffff:ffff",
		"192.0.0.0/29": "192.0.0.7",
	} {
		if got := lastAddr(netip.MustParsePrefix(in)).String(); got != want {
			t.Errorf("lastAddr(%s) = %s, want %s", in, got, want)
		}
	}
}
