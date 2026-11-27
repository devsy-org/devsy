package netcheck

import (
	"go/parser"
	"go/token"
	"net"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type addrCase struct {
	addr    string
	never   bool
	private bool
}

var addrCases = []addrCase{
	{"0.0.0.0", true, false},
	{"0.255.255.255", true, false},
	{"1.0.0.0", false, false},
	{"126.255.255.255", false, false},
	{"127.0.0.1", true, false},
	{"127.255.255.255", true, false},
	{"128.0.0.0", false, false},
	{"169.253.255.255", false, false},
	{"169.254.0.0", true, false},
	{"169.254.169.254", true, false},
	{"169.254.255.255", true, false},
	{"169.255.0.0", false, false},
	{"100.100.100.200", true, true},
	{"168.63.129.16", true, false},
	{"192.0.0.0", true, false},
	{"192.0.0.192", true, false},
	{"192.0.0.255", true, false},
	{"192.0.1.0", false, false},
	{"192.88.98.255", false, false},
	{"192.88.99.0", true, false},
	{"192.88.99.255", true, false},
	{"192.88.100.0", false, false},
	{"223.255.255.255", false, false},
	{"224.0.0.1", true, false},
	{"239.255.255.255", true, false},
	{"240.0.0.0", true, false},
	{"255.255.255.255", true, false},
	{"8.8.8.8", false, false},
	{"9.255.255.255", false, false},
	{"10.0.0.0", false, true},
	{"10.255.255.255", false, true},
	{"11.0.0.0", false, false},
	{"100.63.255.255", false, false},
	{"100.64.0.0", false, true},
	{"100.127.255.255", false, true},
	{"100.128.0.0", false, false},
	{"172.15.255.255", false, false},
	{"172.16.0.0", false, true},
	{"172.31.255.255", false, true},
	{"172.32.0.0", false, false},
	{"192.167.255.255", false, false},
	{"192.168.0.1", false, true},
	{"192.168.255.255", false, true},
	{"192.169.0.0", false, false},
	{"192.0.2.1", false, true},
	{"198.51.100.1", false, true},
	{"203.0.113.1", false, true},
	{"198.17.255.255", false, false},
	{"198.18.0.0", false, true},
	{"198.19.255.255", false, true},
	{"198.20.0.0", false, false},
	{"::", true, false},
	{"::1", true, false},
	{"::808:808", true, false},
	{"::ffff:127.0.0.1", true, false},
	{"::ffff:169.254.169.254", true, false},
	{"::ffff:10.0.0.1", false, true},
	{"::ffff:8.8.8.8", false, false},
	{"::ffff:0:808:808", true, false},
	{"64:ff9b::808:808", true, false},
	{"fd20:ce::254", true, true},
	{"fd20:ce::253", false, true},
	{"fd20:ce::255", false, true},
	{"3ffe:ffff:ffff:ffff:ffff:ffff:ffff:ffff", false, false},
	{"3fff::", false, true},
	{"3fff:fff:ffff:ffff:ffff:ffff:ffff:ffff", false, true},
	{"3fff:1000::", false, false},
	{"64:ff9b:1::1", true, false},
	{"64:ff9a:ffff::1", false, false},
	{"2001:0:4136:e378:8000:63bf:3fff:fdd2", true, false},
	{"2001:1::1", false, false},
	{"2001:4860:4860::8888", false, false},
	{"2001:db8::1", false, true},
	{"2002:808:808::1", true, false},
	{"2003::1", false, false},
	{"ff02::1", true, false},
	{"fe80::1", true, false},
	{"febf:ffff::1", true, false},
	{"fec0::1", true, false},
	{"100::1", true, false},
	{"100:0:0:1::1", true, false},
	{"100:0:0:2::", false, false},
	{"fd00:ec2::254", true, true},
	{"fc00::1", false, true},
	{"fdff:ffff::1", false, true},
	{"fe00::1", false, false},
	{"2606:4700::1111", false, false},
	{"100.100.100.199", false, true},
	{"100.100.100.201", false, true},
	{"168.63.129.15", false, false},
	{"168.63.129.17", false, false},
	{"::1:0:0", false, false},
	{"::8000:0", true, false},
	{"::ffff:1:0:0", false, false},
	{"::ffff:0:8000:0", true, false},
	{"64:ff9b::1:0:0", false, false},
	{"64:ff9b::8000:0", true, false},
	{"64:ff9b:0:1::1", false, false},
	{"64:ff9b:1:8000::1", true, false},
	{"2001:0:8000::1", true, false},
	{"2001:8000::1", false, false},
	{"ff80::1", true, false},
	{"100::8000:0:0:0", true, false},
	{"192.0.1.255", false, false},
	{"192.0.3.1", false, false},
	{"192.0.2.200", false, true},
	{"198.51.101.1", false, false},
	{"198.51.100.200", false, true},
	{"203.0.112.255", false, false},
	{"203.0.114.1", false, false},
	{"203.0.113.200", false, true},
	{"2001:db9::1", false, false},
	{"2001:db8:8000::1", false, true},
}

func TestAddressRanges(t *testing.T) {
	for _, c := range addrCases {
		addr := netip.MustParseAddr(c.addr)
		if got := NeverAllowed(addr); got != c.never {
			t.Errorf("NeverAllowed(%s) = %v, want %v", c.addr, got, c.never)
		}
		if got := Private(addr); got != c.private {
			t.Errorf("Private(%s) = %v, want %v", c.addr, got, c.private)
		}
	}
}

func TestInvalidAndZonedAddressesFailClosed(t *testing.T) {
	for _, addr := range []netip.Addr{
		{},
		netip.MustParseAddr("fe80::1%eth0"),
		netip.MustParseAddr("2606:4700::1111%eth0"),
	} {
		if !NeverAllowed(addr) || !Private(addr) {
			t.Errorf("%q must fail closed", addr.String())
		}
	}
}

const (
	loopback     = "127.0.0.0/8"
	ianaMetadata = "192.0.0.192/32"
	gceMetadata  = "fd20:ce::254/128"
)

func TestOverlapsNeverAllowed(t *testing.T) {
	cases := map[string]string{
		loopback:              loopback,
		"127.1.0.0/16":        loopback,
		"0.0.0.0/7":           thisNetwork,
		"169.254.169.254/32":  linkLocal4,
		ianaMetadata:          ianaMetadata,
		"192.0.0.0/24":        ianaMetadata,
		"::ffff:10.0.0.0/104": "::ffff:0.0.0.0/96",
		"2002:1::/32":         "2002::/16",
		"fec0::/16":           "fec0::/10",
		"fd00:ec2::254/128":   "fd00:ec2::/32",
		gceMetadata:           gceMetadata,
		"fd20:ce::/32":        gceMetadata,
		dummy6:                dummy6,
		"2001:2::/47":         bench6,
		"5f00::/15":           "5f00::/16",
		"0.0.0.0/0":           thisNetwork,
		"::/0":                "::/96",
		"128.0.0.0/1":         linkLocal4,
		"127.0.0.1/8":         loopback,
		"100::/65":            "100::/64",
		"2001:0:0:1::/64":     "2001::/32",
		"ff00::/7":            "ff00::/8",
	}
	for in, want := range cases {
		got, overlaps := OverlapsNeverAllowed(netip.MustParsePrefix(in))
		if !overlaps || got.String() != want {
			t.Errorf("OverlapsNeverAllowed(%s) = %s, %v, want %s", in, got, overlaps, want)
		}
	}
	for _, in := range []string{
		private10, "172.16.0.0/12", "192.168.0.0/16", "8.8.8.0/24",
		"2001:db8::/32", "2606:4700::/32", "192.0.1.0/24", "192.88.100.0/24",
		"126.0.0.0/8", "169.253.0.0/16", "169.255.0.0/16", "223.0.0.0/8",
		"2001:1::/32", "100:0:0:2::/64", "fd20:cf::/32", "3fff::/20",
	} {
		if got, overlaps := OverlapsNeverAllowed(netip.MustParsePrefix(in)); overlaps {
			t.Errorf("OverlapsNeverAllowed(%s) reports %s", in, got)
		}
	}
	if _, overlaps := OverlapsNeverAllowed(netip.Prefix{}); !overlaps {
		t.Error("an invalid prefix must fail closed")
	}
}

// reference classifies with the net package and its own CIDR lists, so a
// mistake in the netip tables or in the unmap step shows up as a difference.
var (
	referenceNever = referenceNets(
		thisNetwork, "127.0.0.0/8", linkLocal4, "100.100.100.200/32",
		"168.63.129.16/32", "192.0.0.0/24", "192.88.99.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"::/96", "::ffff:0:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48", "2001::/32",
		"2002::/16", "ff00::/8", "fe80::/10", "fec0::/10", "100::/64", "fd00:ec2::/32",
		gceMetadata, dummy6, bench6, "5f00::/16",
	)
	referencePrivate = referenceNets(
		private10, "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10",
		"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "198.18.0.0/15",
		"fc00::/7", "2001:db8::/32", "3fff::/20",
	)
)

func referenceNets(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, len(cidrs))
	for i, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		out[i] = n
	}
	return out
}

func referenceHas(nets []*net.IPNet, ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func FuzzAgainstReference(f *testing.F) {
	for _, c := range addrCases {
		f.Add(netip.MustParseAddr(c.addr).AsSlice())
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		addr, ok := netip.AddrFromSlice(raw)
		if !ok {
			t.Skip()
		}
		ip := net.IP(raw)
		if got, want := NeverAllowed(addr), referenceHas(referenceNever, ip); got != want {
			t.Fatalf("NeverAllowed(%s) = %v, reference %v", addr, got, want)
		}
		if got, want := Private(addr), referenceHas(referencePrivate, ip); got != want {
			t.Fatalf("Private(%s) = %v, reference %v", addr, got, want)
		}
	})
}

func TestImportsOnlyTheStandardLibrary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources: %v", err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		for _, path := range importPaths(t, file) {
			if first, _, _ := strings.Cut(path, "/"); strings.Contains(first, ".") {
				t.Errorf("%s imports %s", file, path)
			}
		}
	}
}

func importPaths(t *testing.T, file string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, imp := range parsed.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths
}
