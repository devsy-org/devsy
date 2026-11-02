//go:build linux

package ghfixture

import (
	"context"
	"net/http"
	"time"
)

func (c *checks) checkRelayHosts() bool {
	client := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		Timeout:   2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer client.CloseIdleConnections()
	for _, path := range []string{"/ready", "/owner", "/github-credentials", "/git-credentials"} {
		ctx, cancel := context.WithTimeout(c.ctx, 2*time.Second)
		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			"http://127.0.0.1:12049"+path,
			nil,
		)
		if err != nil {
			cancel()
			return false
		}
		req.Host = "attacker.invalid:12049"
		response, err := client.Do(req)
		ok := err == nil && response.StatusCode == http.StatusForbidden
		if response != nil {
			_ = response.Body.Close()
		}
		cancel()
		if !c.verify("relay rejects non-loopback Host: "+path, ok) {
			return false
		}
	}
	return true
}
