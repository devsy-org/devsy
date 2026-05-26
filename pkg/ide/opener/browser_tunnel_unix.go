//go:build !windows

package opener

import (
	"fmt"
	"net"
	"os"

	"github.com/docker/go-connections/nat"
)

// held pairs a listener with its dup'd file handle so both can be closed
// after the helper has been forked.
type held struct {
	l net.Listener
	f *os.File
}

// prepareInheritedListeners binds each entry in extraPorts on the parent and
// returns the resulting *os.File handles (to attach via cmd.ExtraFiles),
// helper CLI args (--inherit-listener host:port=fd), and a cleanup that
// closes the parent's listener+file handles after cmd.Start().
//
// On Unix only: the helper inherits these fds at fd 3, 4, ... (in order).
// If binding any port fails the function returns an error and rolls back
// any partial state; callers may fall back to the legacy path.
func prepareInheritedListeners(extraPorts []string) (inheritedListenerSetup, error) {
	var holds []held
	var files []*os.File
	var args []string

	cleanup := func() {
		for _, h := range holds {
			if h.f != nil {
				_ = h.f.Close()
			}
			if h.l != nil {
				_ = h.l.Close()
			}
		}
	}

	failure := func(err error) (inheritedListenerSetup, error) {
		cleanup()
		return inheritedListenerSetup{Cleanup: func() {}}, err
	}

	for _, port := range extraPorts {
		parsed, perr := nat.ParsePortSpec(port)
		if perr != nil {
			return failure(fmt.Errorf("parse port %q: %w", port, perr))
		}
		for _, parsedPort := range parsed {
			h, hostAddr, herr := bindInheritedListener(parsedPort)
			if herr != nil {
				return failure(herr)
			}
			// fd 3 is the first ExtraFiles entry in the child.
			childFD := 3 + len(files)
			files = append(files, h.f)
			args = append(args, "--inherit-listener", fmt.Sprintf("%s=%d", hostAddr, childFD))
			holds = append(holds, h)
		}
	}
	return inheritedListenerSetup{Files: files, Args: args, Cleanup: cleanup}, nil
}

// bindInheritedListener binds a single parsed port and returns the held
// listener+file plus the resolved host:port string.
func bindInheritedListener(parsedPort nat.PortMapping) (held, string, error) {
	hostIP := parsedPort.Binding.HostIP
	if hostIP == "" {
		hostIP = "localhost"
	}
	hostPort := parsedPort.Binding.HostPort
	if hostPort == "" {
		hostPort = parsedPort.Port.Port()
	}
	hostAddr := hostIP + ":" + hostPort

	l, err := net.Listen("tcp", hostAddr)
	if err != nil {
		return held{}, hostAddr, fmt.Errorf("listen on %s: %w", hostAddr, err)
	}
	tcpListener, ok := l.(*net.TCPListener)
	if !ok {
		_ = l.Close()
		return held{}, hostAddr, fmt.Errorf("listener for %s is not TCP", hostAddr)
	}
	// (*net.TCPListener).File() returns a duplicated fd; the parent must
	// close both its listener and this file after cmd.Start.
	f, err := tcpListener.File()
	if err != nil {
		_ = l.Close()
		return held{}, hostAddr, fmt.Errorf("dup listener fd for %s: %w", hostAddr, err)
	}
	return held{l: l, f: f}, hostAddr, nil
}
