package agentcontainer

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/devsy-org/devsy/pkg/credentials"
)

func (cmd *CredentialsServerCmd) claimPort(ctx context.Context, port int) (net.Listener, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !cmd.ConfigureGitHubHelper {
		return claimPort(port)
	}
	return claimPortAfterDrain(ctx, port)
}

func claimPortAfterDrain(ctx context.Context, port int) (net.Listener, error) {
	claimCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ln, err := claimPort(port)
		if !errors.Is(err, errPortOwnedByAnotherSession) {
			return ln, err
		}
		_, probeErr := credentials.ProbeReadiness(
			claimCtx,
			port,
			credentials.Readiness{GitHubAllowed: true},
		)
		if probeErr == nil {
			return nil, err
		}
		select {
		case <-claimCtx.Done():
			return nil, errors.Join(err, ctx.Err())
		case <-ticker.C:
		}
	}
}
