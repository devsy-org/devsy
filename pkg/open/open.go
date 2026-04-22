package open

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"time"

	devsyhttp "github.com/devsy-org/devsy/pkg/http"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/skratchdot/open-golang/open"
)

// Run opens the given URL in the default application.
// When running inside a Linux AppImage, it sanitizes the environment
// to avoid library conflicts before spawning xdg-open.
func Run(url string) error {
	if isAppImage() {
		return openURLSanitized(url)
	}
	return open.Run(url)
}

// Open opens the given url in the default application, retrying every second until the context is done.
func Open(ctx context.Context, url string) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
			err := tryOpen(ctx, url, Run)
			if err == nil {
				return nil
			}
		}
	}
}

// JLabDesktop opens the given url in the JLab desktop application, retrying every second until the context is done.
func JLabDesktop(ctx context.Context, url string) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
			err := tryOpen(ctx, url, jlabOpen)
			if err == nil {
				return nil
			}
		}
	}
}

func jlabOpen(url string) error {
	return exec.Command("jlab", url).Run()
}

func tryOpen(ctx context.Context, url string, fn func(string) error) error {
	if err := probeURL(ctx, url); err != nil {
		return err
	}
	return openAfterDelay(ctx, url, fn)
}

func probeURL(ctx context.Context, url string) error {
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

	if resp == nil {
		return fmt.Errorf("not reachable")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusBadGateway ||
		resp.StatusCode == http.StatusServiceUnavailable {
		return fmt.Errorf("not reachable")
	}

	return nil
}

func openAfterDelay(ctx context.Context, url string, fn func(string) error) error {
	select {
	case <-ctx.Done():
		return nil
	case <-time.After(time.Second):
	}

	if err := fn(url); err != nil {
		return fmt.Errorf("open url: %w", err)
	}

	log.Infof("opened url: url=%s", url)
	return nil
}
