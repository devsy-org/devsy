// Package proutil contains shared helpers used by the 'devsy pro' command and
// its resource sub-groups (workspace, cluster, project, template). It exists to
// avoid an import cycle between cmd/pro and its child packages.
package proutil

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/pkg/config"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
)

// Shared table headers used across pro resource listings.
const (
	HeaderName        = "Name"
	HeaderDisplayName = "Display Name"
)

// FindProProvider resolves the devsy config and provider config for the given
// pro host.
func FindProProvider(
	ctx context.Context,
	context, provider, host string,
) (*config.Config, *providerpkg.ProviderConfig, error) {
	devsyConfig, err := config.LoadConfig(context, provider)
	if err != nil {
		return nil, nil, err
	}

	pCfg, err := workspace.ProviderFromHost(ctx, devsyConfig, host)
	if err != nil {
		return devsyConfig, nil, fmt.Errorf("load provider: %w", err)
	}

	return devsyConfig, pCfg, nil
}
