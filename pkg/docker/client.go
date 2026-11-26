package docker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/devsy-org/devsy/pkg/log"
	dockerclient "github.com/docker/docker/client"
)

// Client is a client for docker.
type Client struct {
	dockerclient.CommonAPIClient

	stdio *StdioTransport
}

// NewClient creates a new docker client.
func NewClient(ctx context.Context) (*Client, error) {
	cli, err := newDockerClientFromEnvironment()
	if err != nil {
		log.Warnf("Error creating docker client from environment: %v", err)

		// Last try to create it without the environment option
		cli, err = newDockerClient()
		if err != nil {
			return nil, fmt.Errorf("cannot create docker client: %w", err)
		}
	}

	cli.NegotiateAPIVersion(ctx)
	return cli, nil
}

func NewClientWithInvocation(ctx context.Context, invocation *DockerInvocation) (*Client, error) {
	if err := invocation.validate(); err != nil {
		return nil, err
	}

	// Host is unused: every connection goes through the stdio dialer.
	transport := NewStdioTransport(invocation)
	apiVersion, _ := invocation.Lookup(dockerclient.EnvOverrideAPIVersion)
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.WithVersion(apiVersion),
		dockerclient.WithHTTPClient(&http.Client{
			Transport: &http.Transport{DialContext: transport.DialContext},
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("create docker client over dial-stdio: %w", err)
	}

	cli.NegotiateAPIVersion(ctx)
	return &Client{CommonAPIClient: cli, stdio: transport}, nil
}

// DialHijack aborts only its own pending upgrade when ctx is cancelled; once the
// upgrade succeeds the connection is no longer tied to ctx.
func (c *Client) DialHijack(
	ctx context.Context,
	url, proto string,
	meta map[string][]string,
) (net.Conn, error) {
	if c.stdio == nil {
		return c.CommonAPIClient.DialHijack(ctx, url, proto, meta)
	}

	attempt := &hijackAttempt{}
	negotiated := make(chan struct{})
	defer close(negotiated)
	go func() {
		select {
		case <-ctx.Done():
			attempt.abort()
		case <-negotiated:
		}
	}()

	conn, err := c.CommonAPIClient.DialHijack(
		context.WithValue(ctx, hijackAttemptKey{}, attempt), url, proto, meta,
	)
	if attempt.finish() {
		if conn != nil {
			_ = conn.Close()
		}
		return nil, fmt.Errorf("docker hijack %s aborted: %w", url, context.Cause(ctx))
	}
	return conn, err
}

// Close releases the SDK client and every dial-stdio process it still owns.
func (c *Client) Close() error {
	err := c.CommonAPIClient.Close()
	if c.stdio != nil {
		err = errors.Join(err, c.stdio.Close())
	}
	return err
}

func newDockerClient() (*Client, error) {
	cli, err := dockerclient.NewClientWithOpts()
	if err != nil {
		return nil, fmt.Errorf("could not create docker client: %w", err)
	}

	return &Client{
		CommonAPIClient: cli,
	}, nil
}

func newDockerClientFromEnvironment() (*Client, error) {
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("could not create docker client: %w", err)
	}

	return &Client{
		CommonAPIClient: cli,
	}, nil
}
