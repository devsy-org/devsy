package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/platformclient"
)

// platformCredentials returns the gateway login when the active config is a
// platform login, and nil otherwise. A login missing its URL or key is treated
// as no login, so a half-written config falls back to local workspaces.
func platformCredentials(cfg *config.Config) *config.GatewayCredentials {
	if cfg == nil || cfg.Gateway == nil || cfg.Gateway.URL == "" || cfg.Gateway.APIKey == "" {
		return nil
	}
	return cfg.Gateway
}

func newPlatformClient(creds *config.GatewayCredentials) *platformclient.Client {
	return platformclient.New(creds.URL, nil)
}

func listPlatformWorkspaces(
	ctx context.Context,
	out io.Writer,
	creds *config.GatewayCredentials,
	mode string,
) error {
	workspaces, err := newPlatformClient(creds).ListWorkspaces(ctx, creds.APIKey)
	if err != nil {
		return fmt.Errorf("listing workspaces: %w", err)
	}
	sort.SliceStable(workspaces, func(i, j int) bool {
		return workspaces[i].Name < workspaces[j].Name
	})

	if mode == output.ModeJSON {
		if workspaces == nil {
			workspaces = []platformclient.Workspace{}
		}
		return writePlatformJSON(out, workspaces)
	}
	return writePlatformTable(out, workspaces)
}

func writePlatformJSON(out io.Writer, v any) error {
	encoded, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding workspaces: %w", err)
	}
	_, err = fmt.Fprintln(out, string(encoded))
	return err
}

func writePlatformTable(out io.Writer, workspaces []platformclient.Workspace) error {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tPHASE")
	for _, ws := range workspaces {
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", ws.Name, ws.Phase)
	}
	return tw.Flush()
}
