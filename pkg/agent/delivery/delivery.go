package delivery

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
)

type DeliveryPhase int

const (
	PhasePreStart DeliveryPhase = iota
	PhasePostStart
)

func (p DeliveryPhase) String() string {
	switch p {
	case PhasePreStart:
		return "pre-start"
	case PhasePostStart:
		return "post-start"
	default:
		return fmt.Sprintf("unknown(%d)", int(p))
	}
}

type PreStartOptions struct {
	WorkspaceID string
	RunOptions  *driver.RunOptions
	BinaryPath  string
	Arch        string
}

type PostStartOptions struct {
	WorkspaceID      string
	ContainerDetails *config.ContainerDetails
	BinaryPath       string
	Arch             string
}

type AgentDelivery interface {
	Phase() DeliveryPhase
	DeliverPreStart(ctx context.Context, opts PreStartOptions) error
	DeliverPostStart(ctx context.Context, opts PostStartOptions) error
	Cleanup(ctx context.Context, workspaceID string) error
}
