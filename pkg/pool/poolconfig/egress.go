package poolconfig

import (
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/devsy-org/devsy/pkg/pool/netcheck"
)

const (
	EgressDeny = "deny"

	defaultHTTPSPort  = 443
	maxPort           = 65535
	maxHostNameLength = 253
	maxLabelLength    = 63
	minIPv4PrefixBits = 8
	minIPv6PrefixBits = 32
)

type Egress struct {
	Default string      `json:"default"`
	Allow   []AllowRule `json:"allow,omitempty"`
}

// AllowRule permits egress to a host name or an address range. Exactly one
// of Host and CIDR is set.
type AllowRule struct {
	Host  string `json:"host,omitempty"`
	CIDR  string `json:"cidr,omitempty"`
	Ports []int  `json:"ports,omitempty"`
}

var (
	labelPattern  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	letterPattern = regexp.MustCompile(`^[a-z]`)
	// internalSuffixes name cluster, host and cloud internal services, which
	// an allow rule or upstream must never reach by name.
	internalSuffixes = []string{".internal", ".local", ".localdomain", ".svc", ".home.arpa"}
	metadataNames    = []string{"metadata.goog"}
)

// HostMatches reports whether host matches an allow rule host pattern.
// Patterns are lowercase and must be valid allow patterns. A leading "*."
// matches subdomains at any depth, but not the bare domain. Hosts that are not
// valid DNS names never match.
func HostMatches(pattern, host string) bool {
	if !ValidHostPattern(pattern) {
		return false
	}
	host, ok := NormalizeHost(host)
	if !ok {
		return false
	}
	if base, ok := strings.CutPrefix(pattern, "*."); ok {
		return strings.HasSuffix(host, "."+base)
	}
	return host == pattern
}

// ValidHostPattern reports whether pattern is a valid allow rule host.
func ValidHostPattern(pattern string) bool {
	return checkHostPattern(pattern) == ""
}

// NormalizeHost returns host in lowercase with at most one trailing dot
// removed. It refuses anything that is not an ASCII DNS name an allow rule
// could name: IP addresses, empty labels, internal and metadata names.
func NormalizeHost(host string) (string, bool) {
	// Unicode lowercasing maps some non-ASCII runes, such as U+0130, to ASCII.
	if !isASCII(host) {
		return "", false
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if checkHostName(host) != "" {
		return "", false
	}
	return host, true
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func (s *Spec) applyEgressDefaults() {
	if s.Egress.Default == "" {
		s.Egress.Default = EgressDeny
	}
	for i := range s.Egress.Allow {
		if len(s.Egress.Allow[i].Ports) == 0 {
			s.Egress.Allow[i].Ports = []int{defaultHTTPSPort}
		}
	}
}

func (s *Spec) validateEgress(errs *fieldErrors) {
	if s.Egress.Default != EgressDeny {
		errs.add("spec.egress.default", "must be %q", EgressDeny)
	}
	for i, rule := range s.Egress.Allow {
		validateAllowRule(errs, fmt.Sprintf("spec.egress.allow[%d]", i), rule)
	}
	targets := duplicates(s.Egress.Allow, allowTarget)
	addDuplicates(errs, plainPath("spec.egress.allow"), targets,
		"rule's host or cidr; list all its ports in one rule")
}

// allowTarget keys a rule by what it matches, so two spellings of one CIDR
// count as the same target.
func allowTarget(r AllowRule) string {
	if prefix, err := netip.ParsePrefix(r.CIDR); err == nil {
		return "cidr " + prefix.String()
	}
	return r.Host + " " + r.CIDR
}

func validateAllowRule(errs *fieldErrors, path string, rule AllowRule) {
	field, msg := checkAllowTarget(rule)
	if msg != "" {
		errs.add(path+field, "%s", msg)
	}
	for j, port := range rule.Ports {
		if port < 1 || port > maxPort {
			errs.add(fmt.Sprintf("%s.ports[%d]", path, j), "is outside 1 to %d", maxPort)
		}
	}
	addDuplicates(errs, plainPath(path+".ports"), duplicates(rule.Ports, strconv.Itoa), "port")
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
	if msg := checkLabels(strings.Split(name, ".")); msg != "" {
		return msg
	}
	if isInternalName(name) {
		return "must not be an internal or metadata name (" +
			strings.Join(internalSuffixes, ", ") + ", " + strings.Join(metadataNames, ", ") + ")"
	}
	return ""
}

func isInternalName(name string) bool {
	return slices.ContainsFunc(metadataNames, func(m string) bool {
		return name == m || strings.HasSuffix(name, "."+m)
	}) ||
		slices.ContainsFunc(internalSuffixes, func(suffix string) bool {
			return name == suffix[1:] || strings.HasSuffix(name, suffix)
		})
}

func checkLabels(labels []string) string {
	for _, label := range labels {
		if len(label) > maxLabelLength || !labelPattern.MatchString(label) {
			return "is not a valid DNS name"
		}
	}
	// Some resolvers read names such as "127.1" or "0x7f.0x1" as addresses.
	tld := labels[len(labels)-1]
	if !letterPattern.MatchString(tld) || tld == "localhost" {
		return "must end in a top-level domain that starts with a letter, other than localhost"
	}
	return ""
}

// PrefixTooWide reports whether a cidr rule is wider than config accepts.
func PrefixTooWide(prefix netip.Prefix) bool {
	if prefix.Addr().Is4() {
		return prefix.Bits() < minIPv4PrefixBits
	}
	return prefix.Bits() < minIPv6PrefixBits
}

func checkCIDR(value string) string {
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return "is not a valid CIDR"
	}
	if prefix != prefix.Masked() {
		return "must be canonical, with no address bits set past the prefix length"
	}
	if PrefixTooWide(prefix) {
		return fmt.Sprintf("must be /%d or narrower for IPv4 and /%d or narrower for IPv6",
			minIPv4PrefixBits, minIPv6PrefixBits)
	}
	if reserved, overlaps := netcheck.OverlapsNeverAllowed(prefix); overlaps {
		return "overlaps reserved range " + reserved.String() +
			" (loopback, link-local, metadata, multicast, reserved or IPv4 embedded in IPv6)"
	}
	return ""
}
