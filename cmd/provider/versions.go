package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// VersionsCmd holds the cmd flags for `provider versions`.
type VersionsCmd struct {
	*flags.GlobalFlags
	JSON              bool
	IncludePrerelease bool
	NoCache           bool
}

// NewVersionsCmd creates the cobra command for `provider versions`.
func NewVersionsCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &VersionsCmd{GlobalFlags: f}
	versionsCmd := &cobra.Command{
		Use:   "versions [name]",
		Short: "List available upstream versions for a provider",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}
			name, err := resolveProviderName(args, devsyConfig.Current().DefaultProvider)
			if err != nil {
				return err
			}
			versions, err := workspace.ListProviderVersions(
				devsyConfig,
				name,
				workspace.ListVersionsOptions{
					UseCache:          !cmd.NoCache,
					IncludePrerelease: cmd.IncludePrerelease,
				},
			)
			if err != nil {
				return fmt.Errorf("list versions for %s: %w", name, err)
			}
			if cmd.JSON {
				return json.NewEncoder(os.Stdout).Encode(versions)
			}
			return renderVersionsTable(os.Stdout, versions)
		},
		ValidArgsFunction: completeProviderName(cmd),
	}
	versionsCmd.Flags().BoolVar(&cmd.JSON, "json", false, "Output JSON")
	versionsCmd.Flags().BoolVar(&cmd.IncludePrerelease, "prerelease", false, "Include prereleases")
	versionsCmd.Flags().BoolVar(&cmd.NoCache, "no-cache", false, "Bypass the version cache")
	return versionsCmd
}

func completeProviderName(
	cmd *VersionsCmd,
) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(rootCmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return completion.GetProviderSuggestions(
			rootCmd,
			cmd.Context,
			cmd.Provider,
			args,
			toComplete,
			cmd.Owner,
		)
	}
}

func renderVersionsTable(w io.Writer, versions []workspace.ProviderVersion) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "TAG\tPUBLISHED\tSTATUS"); err != nil {
		return err
	}
	for _, v := range versions {
		status := ""
		if v.Current {
			status = "current"
		} else if v.Prerelease {
			status = "prerelease"
		}
		if _, err := fmt.Fprintf(
			tw,
			"%s\t%s\t%s\n",
			v.Tag,
			v.PublishedAt.Format("2006-01-02"),
			status,
		); err != nil {
			return err
		}
	}
	return tw.Flush()
}
