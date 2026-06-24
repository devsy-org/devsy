package docker

import (
	"context"
	"fmt"

	sdkclient "github.com/docker/go-sdk/client"
)

// Client is a client for the Docker daemon API.
//
// It embeds the high-level docker/go-sdk client, which itself embeds the
// low-level moby APIClient. Callers that need raw daemon access (for example,
// the internal BuildKit path via DialHijack) can use it directly.
type Client struct {
	sdkclient.SDKClient
}

// NewClient creates a new Docker daemon client from the environment.
//
// The go-sdk client negotiates the API version and runs a health check against
// the daemon during construction, so a successful return guarantees a usable
// connection.
func NewClient(ctx context.Context) (*Client, error) {
	cli, err := sdkclient.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot create docker client: %w", err)
	}

	return &Client{SDKClient: cli}, nil
}
