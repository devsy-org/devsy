package poolconfig

import (
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strings"
)

const (
	maxPort           = 65535
	maxHostNameLength = 253
	maxLabelLength    = 63
	minIPv4PrefixBits = 8
	minIPv6PrefixBits = 32
)

var (
	labelPattern  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	letterPattern = regexp.MustCompile(`[a-z]`)
)

// reservedPrefixes are never reachable through an allow rule: loopback,
// link-local, cloud metadata, and IPv6 forms that embed an IPv4 address and
// would otherwise slip past the IPv4 entries.
var reservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("100.100.100.200/32"),
	netip.MustParsePrefix("168.63.129.16/32"),
	netip.MustParsePrefix("192.0.0.192/32"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("::ffff:0:0/96"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("fd00:ec2::/32"),
}

// HostMatches reports whether host matches an allow rule host pattern. A
// leading "*." matches any subdomain, but not the bare domain. Hosts that are
// not valid DNS names never match.
func HostMatches(pattern, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if checkHostName(host) != "" {
		return false
	}
	if base, ok := strings.CutPrefix(pattern, "*."); ok {
		return strings.HasSuffix(host, "."+base)
	}
	return host == pattern
}

func (s *Spec) validateEgress(errs *fieldErrors) {
	if s.Egress.Default != EgressDeny {
		errs.add("spec.egress.default", "must be %q", EgressDeny)
	}
	for i, rule := range s.Egress.Allow {
		validateAllowRule(errs, fmt.Sprintf("spec.egress.allow[%d]", i), rule)
	}
}

func validateAllowRule(errs *fieldErrors, path string, rule AllowRule) {
	field, msg := checkAllowTarget(rule)
	if msg != "" {
		errs.add(path+field, "%s", msg)
	}
	for _, port := range rule.Ports {
		if port < 1 || port > maxPort {
			errs.add(path+".ports", "port %d is outside 1 to %d", port, maxPort)
		}
	}
}

func checkAllowTarget(rule AllowRule) (string, string) {
	switch {
	case (rule.Host == "") == (rule.CIDR == ""):
		return "", "must set exactly one of host or cidr"
	case rule.Host != "":
		return ".host", checkHostPattern(rule.Host)
	default:
		return ".cidr", checkCIDR(rule.CIDR)
	}
}

func (s *Spec) hostAllowed(host string) bool {
	for _, rule := range s.Egress.Allow {
		if rule.Host != "" && HostMatches(rule.Host, host) {
			return true
		}
	}
	return false
}

func checkHostPattern(pattern string) string {
	name := strings.TrimPrefix(pattern, "*.")
	if strings.Contains(name, "*") {
		return "wildcards are allowed only as one leading \"*.\""
	}
	return checkHostName(name)
}

func checkHostName(name string) string {
	if net.ParseIP(name) != nil {
		return "use cidr for IP addresses"
	}
	if name != strings.ToLower(name) {
		return "must be lowercase"
	}
	if len(name) > maxHostNameLength || !strings.Contains(name, ".") {
		return "must be a fully qualified name with at least one dot"
	}
	return checkLabels(strings.Split(name, "."))
}

func checkLabels(labels []string) string {
	for _, label := range labels {
		if len(label) > maxLabelLength || !labelPattern.MatchString(label) {
			return "is not a valid DNS name"
		}
	}
	// Some resolvers read names such as "127.1" as addresses.
	tld := labels[len(labels)-1]
	if !letterPattern.MatchString(tld) || tld == "localhost" {
		return "must end in a top-level domain with a letter, other than localhost"
	}
	return ""
}

func checkCIDR(value string) string {
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return "is not a valid CIDR"
	}
	minBits := minIPv6PrefixBits
	if prefix.Addr().Is4() {
		minBits = minIPv4PrefixBits
	}
	if prefix.Bits() < minBits {
		return fmt.Sprintf("must be /%d or narrower for IPv4 and /%d or narrower for IPv6",
			minIPv4PrefixBits, minIPv6PrefixBits)
	}
	for _, reserved := range reservedPrefixes {
		if prefix.Overlaps(reserved) {
			return "overlaps reserved range " + reserved.String() +
				" (loopback, link-local, metadata or IPv4 embedded in IPv6)"
		}
	}
	return ""
}
