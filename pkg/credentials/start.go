package credentials

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	devsyhttp "github.com/devsy-org/devsy/pkg/http"
	"github.com/devsy-org/devsy/pkg/log"
	portpkg "github.com/devsy-org/devsy/pkg/port"
	"github.com/devsy-org/devsy/pkg/random"
)

func StartCredentialsServer(
	ctx context.Context,
	client tunnel.TunnelClient,
) (int, error) {
	port, err := portpkg.FindAvailablePort(random.InRange(13000, 17000))
	if err != nil {
		return 0, err
	}

	go func() {
		err := RunCredentialsServer(ctx, port, client)
		if err != nil {
			log.Errorf("error running git credentials server: error=%v", err)
		}
	}()

	if err := waitForServer(ctx, port); err != nil {
		return 0, err
	}
	return port, nil
}

func waitForServer(ctx context.Context, port int) error {
	maxWait := time.Second * 4
	now := time.Now()
	for {
		err := PingURL(ctx, "http://localhost:"+strconv.Itoa(port))
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		} else {
			log.Debug("credentials server started")
			return nil
		}

		if time.Since(now) > maxWait {
			log.Debug("credentials server did not start in time")
			return fmt.Errorf("credentials server did not start in time")
		}
	}
}

func PingURL(ctx context.Context, url string) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := devsyhttp.GetHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	return nil
}
