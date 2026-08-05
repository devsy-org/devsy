package tunnel

import (
	"context"
	"fmt"

	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/open"
)

// openURLFunc is a test seam for pkg/open.Open, which shells out to a host
// GUI opener and therefore can't run as-is in unit tests.
var openURLFunc = open.Open

// maybeOpenBrowser is called with f's lock held (see forwarder.Forward).
// open.Open blocks polling the URL until reachable or ctx is done, so it
// always runs in its own goroutine.
func (f *forwarder) maybeOpenBrowser(ctx context.Context, port string, attr config2.PortAttribute) {
	if !attr.IsOpenBrowserAction() {
		return
	}
	if attr.IsOpenOnceAction() {
		if f.openedOnce == nil {
			f.openedOnce = map[string]bool{}
		}
		if f.openedOnce[port] {
			return
		}
		f.openedOnce[port] = true
	}

	url := fmt.Sprintf("http://localhost:%s", port)
	go func() {
		if err := openURLFunc(ctx, url); err != nil {
			log.Warnf("could not open browser for forwarded port %s: %v", port, err)
		}
	}()
}
