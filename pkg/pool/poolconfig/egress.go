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
)

var labelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// reservedPrefixes are never reachable through an allow rule: loopback,
// link-local and cloud metadata addresses.
var reservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("fd00:ec2::254/128"),
}

// HostMatches reports whether host matches an allow rule host pattern. A
// leading "*." matches any subdomain, but not the bare domain.
func HostMatches(pattern, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
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
	for label := range strings.SplitSeq(name, ".") {
		if len(label) > maxLabelLength || !labelPattern.MatchString(label) {
			return "is not a valid DNS name"
		}
	}
	return ""
}

func checkCIDR(value string) string {
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return "is not a valid CIDR"
	}
	if prefix.Bits() == 0 {
		return "must not allow every address"
	}
	for _, reserved := range reservedPrefixes {
		if prefix.Overlaps(reserved) {
			return "overlaps loopback, link-local or metadata range " + reserved.String()
		}
	}
	return ""
}
