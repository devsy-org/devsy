package docker

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/devsy-org/devsy/pkg/pool/provider"
)

const (
	iccDisabled = "false"
	optICC      = "com.docker.network.bridge.enable_icc"
	optMasq     = "com.docker.network.bridge.enable_ip_masquerade"
	driverNet   = "bridge"
	proxyPort   = 3128
)

func bridgeName(pool string) string {
	return resourcePrefix + pool + "-out"
}

// ErrNoAddressSpace reports that Docker has no address pool left for another
// network. A default Docker Engine has pools for about 30 networks, so a pool
// with many live sessions reaches this. The fix is a wider
// default-address-pools setting in the daemon config.
var ErrNoAddressSpace = errors.New(
	"docker has no address pool left for another network; widen default-address-pools in the daemon config",
)

// netInfo is the part of docker network inspect the network plan reads.
type netInfo struct {
	ID       string            `json:"Id"`
	Name     string            `json:"Name"`
	Driver   string            `json:"Driver"`
	Internal bool              `json:"Internal"`
	IPv6     bool              `json:"EnableIPv6"`
	Options  map[string]string `json:"Options"`
	Labels   map[string]string `json:"Labels"`
	IPAM     ipam              `json:"IPAM"`
}

type ipam struct {
	Config []ipamConfig `json:"Config"`
}

type ipamConfig struct {
	Subnet  string `json:"Subnet"`
	Gateway string `json:"Gateway"`
}

// subnet is one IPv4 network and the gateway address Docker holds in it.
type subnet struct {
	prefix  netip.Prefix
	gateway netip.Addr
}

func parseNetwork(raw []byte) (netInfo, error) {
	var in netInfo
	if err := json.Unmarshal(raw, &in); err != nil {
		return netInfo{}, fmt.Errorf("docker network inspect: %w", err)
	}
	return in, nil
}

// subnet returns the network's one IPv4 subnet. A network with no subnet, with
// more than one, or with IPv6 is refused, because the address plan assumes a
// single IPv4 range.
func (n netInfo) subnet() (subnet, error) {
	if n.IPv6 {
		return subnet{}, errors.New("network has IPv6 enabled")
	}
	if len(n.IPAM.Config) != 1 {
		return subnet{}, fmt.Errorf("network has %d subnets, want 1", len(n.IPAM.Config))
	}
	return n.IPAM.Config[0].parse()
}

func (c ipamConfig) parse() (subnet, error) {
	prefix, err := netip.ParsePrefix(c.Subnet)
	if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() {
		return subnet{}, errors.New("network subnet is not a canonical IPv4 CIDR")
	}
	gw, err := netip.ParseAddr(c.Gateway)
	if err != nil || !gw.Is4() || !prefix.Contains(gw) {
		return subnet{}, errors.New("network gateway is not inside its subnet")
	}
	return subnet{prefix: prefix, gateway: gw}, nil
}

// checkBridge verifies a network is the pool's shared outbound bridge: it
// carries the pool's shared label and no session label, has the bridge name,
// is a plain bridge that is not internal, blocks traffic between its
// containers, keeps masquerade on and has one IPv4 subnet and no IPv6.
// A network that fails any of this is never adopted.
func (n netInfo) checkBridge(pool string) error {
	if err := n.checkBridgeIdentity(pool); err != nil {
		return err
	}
	switch {
	case n.Driver != driverNet:
		return fmt.Errorf("outbound network driver is %q, want %q", n.Driver, driverNet)
	case n.Internal:
		return errors.New("outbound network is internal and has no route out")
	case n.Options[optICC] != iccDisabled:
		return errors.New("outbound network allows traffic between containers")
	case n.Options[optMasq] == "false":
		return errors.New("outbound network has no address translation")
	}
	_, err := n.subnet()
	return err
}

func (n netInfo) checkBridgeIdentity(pool string) error {
	if n.Name != bridgeName(pool) {
		return errors.New("outbound network name does not match")
	}
	if n.Labels[provider.LabelShared] != pool {
		return errors.New("outbound network does not carry this pool's shared label")
	}
	if _, ok := n.Labels[provider.LabelPool]; ok {
		return errors.New("outbound network carries a session pool label")
	}
	return nil
}

// checkInternal verifies a session network has no route out.
func (n netInfo) checkInternal() error {
	if !n.Internal {
		return errors.New("session network is not internal")
	}
	_, err := n.subnet()
	return err
}

// sidecarAddr returns the first host address after the gateway that is not
// taken. It never returns the network or broadcast address.
func sidecarAddr(s subnet, taken []netip.Addr) (netip.Addr, error) {
	last := lastAddr(s.prefix)
	for a := s.gateway.Next(); a.IsValid() && a.Less(last) && s.prefix.Contains(a); a = a.Next() {
		if !slices.Contains(taken, a) {
			return a, nil
		}
	}
	return netip.Addr{}, errors.New("network has no free address for the sidecar")
}

// lastAddr is the broadcast address of an IPv4 prefix.
func lastAddr(p netip.Prefix) netip.Addr {
	a := p.Masked().Addr().As4()
	host := 32 - p.Bits()
	for i := 3; i >= 0 && host > 0; i-- {
		n := min(host, 8)
		a[i] |= byte(1<<n - 1)
		host -= n
	}
	return netip.AddrFrom4(a)
}

// bindAddress is the proxy's listen address inside the sidecar.
func bindAddress(a netip.Addr) string {
	return netip.AddrPortFrom(a, proxyPort).String()
}

// alwaysDeny lists what the proxy must never dial: every subnet of the pool's
// session networks, the outbound bridge with its gateway, and the sidecar's
// own interface networks. The result is sorted and has no duplicates.
func alwaysDeny(sessionNets []subnet, bridge subnet) []string {
	seen := map[netip.Prefix]bool{}
	add := func(p netip.Prefix) { seen[p] = true }
	add(bridge.prefix)
	add(netip.PrefixFrom(bridge.gateway, 32))
	for _, s := range sessionNets {
		add(s.prefix)
		add(netip.PrefixFrom(s.gateway, 32))
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p.String())
	}
	slices.Sort(out)
	return out
}

// mapCreateError turns Docker's out-of-address-space failure into
// ErrNoAddressSpace and leaves any other error alone.
func mapCreateError(err error) error {
	var ce *CommandError
	if !errors.As(err, &ce) {
		return err
	}
	text := strings.ToLower(ce.Stderr)
	for _, capacity := range capacityMessages {
		if strings.Contains(text, capacity) {
			return errors.Join(ErrNoAddressSpace, err)
		}
	}
	return err
}

// capacityMessages are Docker's out-of-pool errors: the first from Docker 27
// and later, the second from Docker 23 to 26.
var capacityMessages = []string{
	"all predefined address pools have been fully subnetted",
	"could not find an available, non-overlapping ipv4 address pool",
}
