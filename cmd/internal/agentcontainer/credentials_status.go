package agentcontainer

import (
	"context"
	"encoding/json"
	"time"

	"github.com/devsy-org/devsy/pkg/credentials"
	"github.com/spf13/cobra"
)

func NewCredentialsStatusCmd() *cobra.Command {
	var expected credentials.Readiness
	cmd := &cobra.Command{
		Use: "credentials-status", Hidden: true, Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			port, err := credentials.GetPort()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
			defer cancel()
			status := waitCredentialsStatus(ctx, port, expected)
			return json.NewEncoder(c.OutOrStdout()).Encode(status)
		},
	}
	cmd.Flags().StringVar(&expected.Owner, "user", "", "Guest service owner")
	cmd.Flags().StringVar(&expected.HostOwner, "host-owner", "", "Nonsecret host connection owner")
	cmd.Flags().
		BoolVar(&expected.GitHubAllowed, "github-enabled", false, "Expected effective permission")
	return cmd
}

func waitCredentialsStatus(
	ctx context.Context,
	port int,
	expected credentials.Readiness,
) credentials.Readiness {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := credentials.ProbeReadiness(ctx, port, expected)
		if err == nil {
			return status
		}
		select {
		case <-ctx.Done():
			return credentials.Readiness{GitHubState: "unavailable"}
		case <-ticker.C:
		}
	}
}
