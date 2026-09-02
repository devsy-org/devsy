package transport

import "fmt"

// Addr is a stable diagnostic address for a synthetic connection.
type Addr struct {
	NetworkName string
	RemoteName  string
}

func (a Addr) Network() string {
	if a.NetworkName == "" {
		return "devsy-transport"
	}
	return a.NetworkName
}

func (a Addr) String() string {
	if a.RemoteName == "" {
		return a.Network()
	}
	return fmt.Sprintf("%s:%s", a.Network(), a.RemoteName)
}

func NewAddr(remote string) Addr {
	return Addr{NetworkName: "devsy-transport", RemoteName: remote}
}
