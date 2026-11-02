package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

const ReadinessVersion = 1

const (
	// #nosec G101 -- Public HTTP header carrying a nonsecret relay identifier.
	HeaderCredentialsInstance = "X-Devsy-Credentials-Instance"
	// #nosec G101 -- Public HTTP header carrying a nonsecret host identifier.
	HeaderCredentialsHostOwner = "X-Devsy-Credentials-Host-Owner"
)

type Readiness struct {
	Version       int    `json:"version"`
	Owner         string `json:"owner"`
	HostOwner     string `json:"host_owner"`
	Instance      string `json:"instance"`
	GitHubAllowed bool   `json:"github_allowed"`
	GitHubState   string `json:"github_state"`
	Address       string `json:"-"`
}

func (r Readiness) Matches(expected Readiness) bool {
	return r.validIdentity() && r.matchesIdentity(expected) &&
		r.GitHubAllowed == expected.GitHubAllowed &&
		r.terminal()
}

func (r Readiness) validIdentity() bool {
	return r.Version == ReadinessVersion && r.Owner != "" && r.HostOwner != "" && r.Instance != ""
}

func (r Readiness) matchesIdentity(expected Readiness) bool {
	return matchesString(r.Owner, expected.Owner) &&
		matchesString(r.HostOwner, expected.HostOwner) &&
		matchesString(r.Instance, expected.Instance)
}

func matchesString(actual, expected string) bool { return expected == "" || actual == expected }

func (r Readiness) terminal() bool {
	switch r.GitHubState {
	case "installed", "already_installed", "backend_missing", "unavailable", "disabled":
		return true
	default:
		return false
	}
}

func LoopbackHTTPClient() *http.Client {
	return &http.Client{
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func ProbeReadiness(ctx context.Context, port int, expected Readiness) (Readiness, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	results := make(chan struct {
		status Readiness
		err    error
	}, 2)
	for _, host := range []string{"127.0.0.1", "::1"} {
		go func() {
			address := net.JoinHostPort(host, strconv.Itoa(port))
			status, err := probeAddress(ctx, address, expected)
			results <- struct {
				status Readiness
				err    error
			}{status, err}
		}()
	}
	var result Readiness
	var probeErr error
	for range 2 {
		r := <-results
		if r.err == nil && result.Address == "" {
			result = r.status
			cancel()
		}
		probeErr = errors.Join(probeErr, r.err)
	}
	if result.Address != "" {
		return result, nil
	}
	return Readiness{}, probeErr
}

func probeAddress(ctx context.Context, address string, expected Readiness) (Readiness, error) {
	client := LoopbackHTTPClient()
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/ready", nil)
	if err != nil {
		return Readiness{}, err
	}
	response, err := client.Do(req)
	if err != nil {
		return Readiness{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Readiness{}, errors.New("credentials readiness unavailable")
	}
	b, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil {
		return Readiness{}, err
	}
	status, err := decodeReadiness(b, expected)
	if err != nil {
		return Readiness{}, err
	}
	status.Address = address
	return status, nil
}

func decodeReadiness(b []byte, expected Readiness) (Readiness, error) {
	var status Readiness
	if len(b) > 4096 || json.Unmarshal(b, &status) != nil || !status.Matches(expected) {
		return Readiness{}, errors.New("invalid readiness response")
	}
	return status, nil
}
