package docker

import (
	"context"
	"fmt"
	"net/http"

	"github.com/devsy-org/devsy/pkg/log"
	dockerclient "github.com/docker/docker/client"
)

// Client is a client for docker.
type Client struct {
	dockerclient.CommonAPIClient
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
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.WithHTTPClient(&http.Client{
			Transport: &http.Transport{
				DialContext: NewStdioTransport(invocation).DialContext,
			},
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("create docker client over dial-stdio: %w", err)
	}

	cli.NegotiateAPIVersion(ctx)
	return &Client{CommonAPIClient: cli}, nil
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
