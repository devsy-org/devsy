package ts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
)

func GetClientHostname(userName string) (string, error) {
	osHostname, err := os.Hostname()
	if err != nil {
		return "", err
	}
	osHostname = strings.ToLower(strings.ReplaceAll(osHostname, ".", "-"))
	return fmt.Sprintf(config.BinaryName+".%s.%s.client", osHostname, userName), nil
}

func GetWorkspaceHostname(name, namespace string) string {
	return fmt.Sprintf(config.BinaryName+".%s.%s.workspace", name, namespace)
}

func ParseWorkspaceHostname(hostname string) (name string, project string, err error) {
	parts := strings.SplitN(hostname, ".", 4)
	if len(parts) != 4 {
		return name, project, fmt.Errorf("invalid hostname: %s", hostname)
	}

	name = parts[1]
	project = parts[2]

	return name, project, nil
}

func GetURL(host string, port int) string {
	if port == 0 {
		return fmt.Sprintf("%s.%s", host, DevsyTSNetDomain)
	}
	return fmt.Sprintf("%s.%s:%d", host, DevsyTSNetDomain, port)
}

// WaitHostReachable polls until the given host is reachable via ts.
func WaitHostReachable(
	ctx context.Context,
	lc *local.Client,
	addr Addr,
	maxRetries int,
) error {
	port, err := ToUint16Port(addr.Port())
	if err != nil {
		return fmt.Errorf("host %s: %w", addr.String(), err)
	}

	for i := range maxRetries {
		timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		conn, dialErr := lc.DialTCP(timeoutCtx, addr.Host(), port)
		cancel()
		if dialErr == nil {
			_ = conn.Close()
			return nil // Host is reachable
		}
		log.Debugf("host %s not reachable, retrying (%d/%d)", addr.String(), i+1, maxRetries)
		time.Sleep(200 * time.Millisecond)

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}

	return fmt.Errorf("host %s not reachable", addr.String())
}

// ToUint16Port validates that port fits the uint16 range TCP ports use.
func ToUint16Port(port int) (uint16, error) {
	if port < 0 || port > math.MaxUint16 {
		return 0, fmt.Errorf("port %d out of range", port)
	}
	return uint16(port), nil // #nosec G115 -- bounds-checked above
}

// ipnWatcher is the subset of *local.IPNBusWatcher used by watchNetmap.
type ipnWatcher interface {
	Next() (ipn.Notify, error)
}

// WatchNetmap invokes netmapChangedFn whenever the tailnet state changes.
func WatchNetmap(
	ctx context.Context,
	lc *local.Client,
	netmapChangedFn func(status *ipnstate.Status),
) error {
	watcher, err := lc.WatchIPNBus(
		ctx,
		ipn.NotifyInitialStatus|ipn.NotifyWatchEngineUpdates|ipn.NotifyPeerChanges,
	)
	if err != nil {
		return err
	}
	defer func() { _ = watcher.Close() }()

	return watchNetmap(ctx, watcher, lc.Status, netmapChangedFn)
}

func watchNetmap(
	ctx context.Context,
	watcher ipnWatcher,
	fetchStatus func(context.Context) (*ipnstate.Status, error),
	netmapChangedFn func(status *ipnstate.Status),
) error {
	trigger := make(chan struct{}, 1)
	errc := make(chan error, 1)

	go drainNotifications(watcher, trigger, errc)

	for {
		select {
		case err := <-errc:
			return err
		case <-trigger:
			status, err := fetchStatus(ctx)
			if err != nil {
				return fmt.Errorf("fetch status: %w", err)
			}
			netmapChangedFn(status)
		}
	}
}

func drainNotifications(watcher ipnWatcher, trigger chan<- struct{}, errc chan<- error) {
	for {
		n, err := watcher.Next()
		if err != nil {
			errc <- fmt.Errorf("watch ipn: %w", err)
			return
		}
		if n.ErrMessage != nil {
			errc <- fmt.Errorf("tailscale error: %w", errors.New(*n.ErrMessage))
			return
		}
		if !netmapChanged(n) {
			continue
		}
		select {
		case trigger <- struct{}{}:
		default:
		}
	}
}

// netmapChanged reports whether a notification indicates a tailnet
// state change.
func netmapChanged(n ipn.Notify) bool {
	return n.InitialStatus != nil || n.SelfChange != nil ||
		len(n.PeersChanged) > 0 || len(n.PeersRemoved) > 0 ||
		len(n.PeerChangedPatch) > 0
}

// netmapFileName is the debug snapshot both the daemon and workspace
// server's WatchNetmap callbacks write on every tailnet state change.
const netmapFileName = "netmap.json"

// PersistNetmapStatus marshals status and writes it to netmap.json under
// rootDir for debugging, logging rather than returning any failure since
// callers invoke this from a WatchNetmap callback with no error path.
func PersistNetmapStatus(rootDir string, status *ipnstate.Status) {
	nm, err := json.Marshal(status)
	if err != nil {
		log.Errorf("failed to marshal netmap: %v", err)
		return
	}
	if err := os.WriteFile(filepath.Join(rootDir, netmapFileName), nm, 0o600); err != nil {
		log.Errorf("failed to write netmap: %v", err)
	}
}
