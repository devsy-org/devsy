package tunnel

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/netstat"
	portpkg "github.com/devsy-org/devsy/pkg/port"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
	"golang.org/x/crypto/ssh"
)

// PortAttributeResolver resolves port attributes for a given port string.
type PortAttributeResolver func(port string) config2.PortAttribute

// newForwarder returns a new forwarder using an SSH client and list of ports to forward,
// for each port a new go routine is used to manage the SSH channel.
func newForwarder(
	sshClient *ssh.Client,
	forwardedPorts []string,
	resolver PortAttributeResolver,
) netstat.Forwarder {
	return &forwarder{
		sshClient:      sshClient,
		forwardedPorts: forwardedPorts,
		portMap:        map[string]context.CancelFunc{},
		resolver:       resolver,
	}
}

// forwarder multiplexes a SSH client to forward ports to the remote container.
type forwarder struct {
	sync.Mutex

	sshClient      *ssh.Client
	forwardedPorts []string
	resolver       PortAttributeResolver

	portMap map[string]context.CancelFunc
}

// Forward opens an SSH channel in the existing connection with channel type "direct-tcpip" to forward the local port.
func (f *forwarder) Forward(port string, _ netstat.PortForwardAttribute) error {
	f.Lock()
	defer f.Unlock()

	if f.isExcluded(port) || f.portMap[port] != nil {
		return nil
	}

	attr := f.resolveAttr(port)
	if !attr.ShouldAutoForward() {
		log.Debugf("Skipping port %s: onAutoForward=ignore", port)
		return nil
	}

	localAddr := "localhost:" + port
	if attr.RequireLocalPort {
		if ok, _ := portpkg.IsAvailable(localAddr); !ok {
			log.Warnf("Port %s required but unavailable, skipping forward", port)
			return fmt.Errorf("required local port %s unavailable", port)
		}
	}

	if attr.ElevateIfNeeded {
		portNum, _ := strconv.Atoi(port)
		if portNum < 1024 {
			if runtime.GOOS == "linux" {
				log.Warnf(
					"Port %s requires elevation (elevateIfNeeded=true); privileged port binding not supported in tunnel mode",
					port,
				)
			} else {
				log.Warnf("Port %s: elevateIfNeeded is only applicable on Linux", port)
			}
		}
	}

	cancelCtx, cancel := context.WithCancel(context.Background())
	f.portMap[port] = cancel

	parts := []string{}
	if attr.Label != "" {
		parts = append(parts, attr.Label)
	}
	if attr.Protocol != "" {
		parts = append(parts, attr.Protocol)
	}
	if len(parts) > 0 {
		log.Infof("Start port-forwarding on port %s (%s)", port, strings.Join(parts, ", "))
	} else {
		log.Infof("Start port-forwarding on port %s", port)
	}

	go func(port string) {
		network := "tcp"
		err := devssh.PortForward(
			cancelCtx,
			f.sshClient,
			network,
			localAddr,
			network,
			"localhost:"+port,
			0,
		)
		if err != nil {
			log.Errorf("Error port forwarding %s: %v", port, err)
		}
	}(port)

	return nil
}

// StopForward stops the port forwarding for the given port.
func (f *forwarder) StopForward(port string) error {
	f.Lock()
	defer f.Unlock()

	if f.isExcluded(port) || f.portMap[port] == nil {
		return nil
	}

	attr := f.resolveAttr(port)
	label := attr.Label
	if label != "" {
		log.Infof("Stop port-forwarding on port %s (%s)", port, label)
	} else {
		log.Infof("Stop port-forwarding on port %s", port)
	}
	f.portMap[port]()
	delete(f.portMap, port)

	return nil
}

func (f *forwarder) isExcluded(port string) bool {
	return slices.Contains(f.forwardedPorts, port)
}

func (f *forwarder) resolveAttr(port string) config2.PortAttribute {
	if f.resolver == nil {
		return config2.PortAttribute{}
	}
	return f.resolver(port)
}

// NewPortAttributeResolver builds a resolver from the devcontainer config.
func NewPortAttributeResolver(
	portsAttrs map[string]config2.PortAttribute,
	fallback *config2.PortAttribute,
) PortAttributeResolver {
	return func(port string) config2.PortAttribute {
		portNum, err := strconv.Atoi(port)
		if err != nil {
			return config2.PortAttribute{}
		}
		return config2.ResolvePortAttribute(portNum, portsAttrs, fallback)
	}
}
