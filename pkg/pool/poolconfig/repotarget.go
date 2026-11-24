package poolconfig

import (
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

const (
	errRepoScheme = "must use https://, ssh:// or git@host:path"
	errRepoHost   = "must not name a local, private, reserved or metadata host"
)

var (
	// blockedRepoPrefixes cover loopback, private, link-local, CGNAT,
	// documentation (including 3fff::/20), multicast and reserved ranges, cloud metadata
	// addresses, and the IPv6 forms that embed an IPv4 address.
	blockedRepoPrefixes = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("168.63.129.16/32"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("192.88.99.0/24"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("224.0.0.0/4"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("::/96"),
		netip.MustParsePrefix("::ffff:0:0:0/96"),
		netip.MustParsePrefix("64:ff9b::/96"),
		netip.MustParsePrefix("64:ff9b:1::/48"),
		netip.MustParsePrefix("100::/64"),
		netip.MustParsePrefix("2001::/23"),
		netip.MustParsePrefix("2001:db8::/32"),
		netip.MustParsePrefix("2002::/16"),
		netip.MustParsePrefix("3fff::/20"),
		netip.MustParsePrefix("fc00::/7"),
		netip.MustParsePrefix("fe80::/10"),
		netip.MustParsePrefix("fec0::/10"),
		netip.MustParsePrefix("ff00::/8"),
	}
	repoHostPattern = regexp.MustCompile(`^([a-z0-9-]+\.)*[a-z0-9-]+\.?$`)
	metadataHosts   = []string{
		"metadata", "metadata.google.internal", "metadata.goog",
		"instance-data", "instance-data.ec2.internal",
	}
)

type repoTarget struct {
	host, user, path, port string
}

// repoTargetError checks where a normalized reference points. A host, user
// or path starting with - would reach git or ssh as an option.
func repoTargetError(raw, normalized string) string {
	if strings.HasPrefix(raw, "-") {
		return errRepoURL
	}
	t, msg := parseRepoTarget(normalized)
	if msg != "" {
		return msg
	}
	if strings.HasPrefix(t.host, "-") || strings.HasPrefix(t.user, "-") ||
		strings.HasPrefix(strings.TrimLeft(t.path, "/"), "-") {
		return errRepoURL
	}
	if !validRepoPort(t.port) {
		return errRepoURL
	}
	return repoHostError(t.host)
}

// validRepoPort accepts no port or one in 1 to 65535.
func validRepoPort(port string) bool {
	if port == "" {
		return true
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= maxPort
}

func parseRepoTarget(normalized string) (repoTarget, string) {
	if rest, ok := strings.CutPrefix(normalized, scpPrefix); ok {
		t := repoTarget{user: "git"}
		if bracketed, ok := strings.CutPrefix(rest, "["); ok {
			if t.host, t.path, ok = strings.Cut(bracketed, "]:"); !ok {
				return t, errRepoURL
			}
			return t, ""
		}
		t.host, t.path, _ = strings.Cut(rest, ":")
		return t, ""
	}
	if !strings.HasPrefix(normalized, "https://") && !strings.HasPrefix(normalized, "ssh://") {
		return repoTarget{}, errRepoScheme
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return repoTarget{}, errRepoURL
	}
	return repoTarget{host: u.Hostname(), user: u.User.Username(), path: u.Path, port: u.Port()}, ""
}

// repoHostError checks a name by its IDNA lookup form, the one a resolver
// sees, so fullwidth or ideographic spellings of localhost, a metadata name
// or an address get the same answer as the ASCII spelling.
func repoHostError(host string) string {
	if addr, err := netip.ParseAddr(strings.TrimSuffix(host, ".")); err == nil {
		return repoAddrError(addr)
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil || !repoHostPattern.MatchString(ascii) {
		return errRepoURL
	}
	ascii = strings.TrimSuffix(ascii, ".")
	if addr, err := netip.ParseAddr(ascii); err == nil {
		return repoAddrError(addr)
	}
	if !hasNameTLD(ascii) {
		return errRepoURL
	}
	if isLocalName(ascii) {
		return errRepoHost
	}
	return ""
}

// isLocalName covers names a resolver answers from the local network: a
// single label (search domains), the egress internal suffixes and metadata names.
func isLocalName(name string) bool {
	return name == "localhost" || strings.HasSuffix(name, ".localhost") ||
		!strings.Contains(name, ".") || isInternalName(name) ||
		slices.Contains(metadataHosts, name)
}

func repoAddrError(addr netip.Addr) string {
	addr = addr.WithZone("").Unmap()
	if slices.ContainsFunc(blockedRepoPrefixes, func(p netip.Prefix) bool {
		return p.Contains(addr)
	}) {
		return errRepoHost
	}
	return ""
}

// hasNameTLD refuses numeric hosts that are not plain IP literals, such as
// 127.1 or 0x7f.1, which the resolver reads as addresses.
func hasNameTLD(host string) bool {
	last := host[strings.LastIndexByte(host, '.')+1:]
	return last != "" && last[0] >= 'a' && last[0] <= 'z'
}
