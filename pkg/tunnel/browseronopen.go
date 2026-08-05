package tunnel

import (
	"context"
	"fmt"

	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/open"
)

var openURLFunc = open.Open

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
