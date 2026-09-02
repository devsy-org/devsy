package transport

import "fmt"

const networkName = "devsy-transport"

// Addr is a stable diagnostic address for a synthetic connection.
type Addr struct {
	NetworkName string
	RemoteName  string
}

func NewAddr(remote string) Addr {
	return Addr{NetworkName: networkName, RemoteName: remote}
}

func (a Addr) Network() string {
	if a.NetworkName == "" {
		return networkName
	}
	return a.NetworkName
}

func (a Addr) String() string {
	if a.RemoteName == "" {
		return a.Network()
	}
	return fmt.Sprintf("%s:%s", a.Network(), a.RemoteName)
}
