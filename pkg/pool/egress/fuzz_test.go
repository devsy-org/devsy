package egress

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
)

// referenceName is a plain re-statement of the name rules for the two rules
// the fuzz policy holds, written without the poolconfig helpers.
func referenceName(host string) bool {
	if !isASCII(host) {
		return false
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	labels := strings.Split(host, ".")
	if len(host) > 253 || len(labels) < 2 || !referenceLabels(labels) {
		return false
	}
	return host == hostGitHub || strings.HasSuffix(host, ".example.com")
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func referenceLabels(labels []string) bool {
	for _, l := range labels {
		if !referenceLabel(l) {
			return false
		}
	}
	tld := labels[len(labels)-1][0]
	return tld >= 'a' && tld <= 'z'
}

func referenceLabel(l string) bool {
	if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
		return false
	}
	for i := range len(l) {
		if !referenceChar(l[i]) {
			return false
		}
	}
	return true
}

func referenceChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-'
}

func fuzzPolicy(tb testing.TB, rules ...poolconfig.AllowRule) *Policy {
	tb.Helper()
	return newPolicy(tb, rules, "172.17.0.0/16", "fd00:1::/32")
}

func FuzzCheckName(f *testing.F) {
	for _, seed := range []string{
		hostGitHub, hostSub, "A.EXAMPLE.COM.", "example.com", "evil-example.com",
		"a..example.com", "g\u0130thub.com", "1.2.3.4", loopback6, "github.com\x00", "127.1",
	} {
		f.Add(seed, 443)
	}
	p := fuzzPolicy(f, host(hostGitHub), host(hostWild))
	f.Fuzz(func(t *testing.T, h string, port int) {
		d := p.CheckName(h, port)
		want := port == 443 && referenceName(h)
		if d.Allow != want {
			t.Fatalf("CheckName(%q, %d) = %v, reference %v", h, port, d, want)
		}
		if d.Allow == (d.Reason != "") {
			t.Fatalf("decision %v has an inconsistent reason", d)
		}
		if _, err := netip.ParseAddr(h); err == nil && d.Allow {
			t.Fatalf("CheckName allowed the address %q", h)
		}
	})
}

var coreNever = prefixes(
	"0.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "224.0.0.0/4", "240.0.0.0/4",
	"::/96", "fe80::/10", "ff00::/8", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16", "2001::/32",
)

func FuzzCheckAddr(f *testing.F) {
	for _, seed := range [][]byte{
		{127, 0, 0, 1},
		{169, 254, 169, 254},
		{10, 0, 0, 1},
		{8, 8, 8, 8},
		{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 255, 255, 127, 0, 0, 1},
		{0x20, 0x02, 8, 8, 8, 8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
	} {
		f.Add(seed, uint8(0))
	}
	all := fuzzPolicy(f, host(hostWild), cidr("0.0.0.0/0"), cidr("::/0"))
	f.Fuzz(func(t *testing.T, raw []byte, zone uint8) {
		addr, ok := netip.AddrFromSlice(raw)
		if !ok {
			return
		}
		if zone%4 == 0 {
			addr = addr.WithZone("eth0")
		}
		for name, d := range map[string]Decision{
			"literal":  all.CheckAddr(addr, 443),
			"resolved": all.CheckResolved(hostSub, 443, []netip.Addr{addr}),
		} {
			if d.Allow && forbidden(addr) {
				t.Fatalf("%s allowed %s", name, addr)
			}
		}
	})
}

func forbidden(addr netip.Addr) bool {
	if addr.Zone() != "" {
		return true
	}
	addr = addr.Unmap()
	for _, n := range coreNever {
		if n.Contains(addr) {
			return true
		}
	}
	for _, n := range prefixes("172.17.0.0/16", "fd00:1::/32") {
		if n.Contains(addr) {
			return true
		}
	}
	return false
}
