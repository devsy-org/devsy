// Package logout implements `devsy logout`, which revokes the stored platform
// API key and removes it from the devsy config.
package logout

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platformclient"
	"github.com/spf13/cobra"
)

// NewLogoutCmd creates the `devsy logout` command.
func NewLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Log out of the Devsy platform gateway",
		Long: "Log out of the Devsy platform gateway.\n\n" +
			"Logout revokes the API key that `devsy login` stored, using the key itself, " +
			"and then removes the key and org from the devsy config file. " +
			"If the gateway cannot revoke the key, the key stays in the config " +
			"so you can run logout again.",
		Example: "  devsy logout",
		Args:    cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return run(cobraCmd.Context(), cobraCmd.OutOrStdout())
		},
	}
}

func run(ctx context.Context, out io.Writer) error {
	cfg, err := config.LoadConfig("", "")
	if err != nil {
		return err
	}
	creds := cfg.Gateway
	if creds == nil || creds.APIKey == "" {
		_, _ = fmt.Fprintln(out, "Not logged in to a platform gateway")
		return nil
	}
	if err := revokeStoredKey(ctx, creds); err != nil {
		return err
	}
	if err := clearLogin(creds); err != nil {
		return fmt.Errorf("removing login from config: %w", err)
	}
	_, _ = fmt.Fprintf(out, "Logged out of %s\n", creds.URL)
	return nil
}

// revokeStoredKey revokes the key with itself as the bearer. A 404 means the
// key is already gone, which is the state logout wants.
func revokeStoredKey(ctx context.Context, creds *config.GatewayCredentials) error {
	if creds.URL == "" || creds.OrgID == "" || creds.KeyRecordID == "" {
		return errors.New(
			"the stored login is incomplete and its key cannot be revoked; " +
				"run 'devsy login' to replace it",
		)
	}
	client := platformclient.New(creds.URL, nil)
	err := client.RevokeAPIKey(ctx, creds.APIKey, creds.OrgID, creds.KeyRecordID)
	if err != nil && !alreadyRevoked(err) {
		return fmt.Errorf("revoking API key at %s: %w", creds.URL, err)
	}
	return nil
}

func alreadyRevoked(err error) bool {
	var apiErr *platformclient.APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
}

// clearLogin removes the key and org from the config but keeps the gateway URL,
// so commands can tell a logged-out user from one who never logged in. It does
// nothing when the config now holds a different key, because a login made since
// the revoke started must not be wiped.
func clearLogin(revoked *config.GatewayCredentials) error {
	return config.UpdateConfig("", "", func(cfg *config.Config) error {
		if cfg.Gateway == nil || cfg.Gateway.KeyRecordID != revoked.KeyRecordID {
			return nil
		}
		cfg.Gateway = &config.GatewayCredentials{URL: cfg.Gateway.URL}
		return nil
	})
}
