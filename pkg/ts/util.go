package ts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
)

// DevsyTSNetDomain is the MagicDNS suffix for the Devsy tailnet.
// The literal value matches the configured Tailscale tailnet and must stay
// in sync with operator network configuration.
const DevsyTSNetDomain = "ts.loft"

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
	for i := range maxRetries {
		timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		conn, err := lc.DialTCP(timeoutCtx, addr.Host(), uint16(addr.Port()))
		if err == nil {
			_ = conn.Close()
			return nil // Host is reachable
		}
		log.Debugf("Host %s not reachable, retrying (%d/%d)", addr.String(), i+1, maxRetries)
		time.Sleep(200 * time.Millisecond)

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}

	return fmt.Errorf("host %s not reachable", addr.String())
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

// watchNetmap drains watcher on a dedicated goroutine so notifications keep
// flowing while fetchStatus is in flight, coalescing any changes that arrive
// during a fetch into a single follow-up call. Without this, a burst of
// notifications (e.g. NotifyPeerChanges deltas) can fill the IPN bus's
// 128-entry queue while netmapChangedFn's caller blocks in fetchStatus,
// causing tailscaled to close the watch ("IPN bus consumer fell behind").
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

// drainNotifications continuously reads watcher.Next(), coalescing every
// relevant change into a non-blocking signal on trigger, until watcher
// reports a terminal error (or a bus-side ErrMessage) on errc.
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

// netmapChanged reports whether a bus notification indicates a tailnet
// state change.
func netmapChanged(n ipn.Notify) bool {
	return n.InitialStatus != nil || n.SelfChange != nil ||
		len(n.PeersChanged) > 0 || len(n.PeersRemoved) > 0 ||
		len(n.PeerChangedPatch) > 0
}
