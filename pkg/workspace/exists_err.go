package workspace

import (
	"context"

	"github.com/devsy-org/devsy/pkg/config"
)

// CheckExists reports whether a matching workspace exists. Unlike Exists it
// returns lookup failures and context cancellation instead of reporting the
// workspace as missing.
func CheckExists(
	ctx context.Context,
	devsyConfig *config.Config,
	opts LookupOptions,
) (bool, error) {
	found, err := findWorkspace(ctx, devsyConfig, opts)
	if err != nil {
		return false, err
	}
	if found != nil {
		return true, nil
	}

	return false, ctx.Err()
}
