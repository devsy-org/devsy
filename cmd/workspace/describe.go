package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/table"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// DescribeCmd holds the cmd flags.
type DescribeCmd struct {
	*flags.GlobalFlags

	Timeout string
}

// describeOutput is the JSON shape: the full workspace config plus live state.
type describeOutput struct {
	*provider.Workspace
	State string `json:"state"`
}

// NewDescribeCmd creates a new command.
func NewDescribeCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &DescribeCmd{
		GlobalFlags: globalFlags,
	}
	describeCmd := &cobra.Command{
		Use:   "describe [flags] [workspace-path|workspace-name]",
		Short: "Shows the full details of a workspace",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.execute(cobraCmd.Context(), args)
		},
		ValidArgsFunction: cmd.validArgs,
	}

	describeCmd.Flags().
		StringVar(&cmd.Timeout, "timeout", "30s", "The timeout to wait until the status can be retrieved")
	return describeCmd
}

// Run runs the command logic.
func (cmd *DescribeCmd) Run(ctx context.Context, client client2.BaseWorkspaceClient) error {
	if cmd.Timeout != "" {
		duration, err := time.ParseDuration(cmd.Timeout)
		if err != nil {
			return fmt.Errorf("parse --timeout: %w", err)
		}

		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, duration)
		defer cancel()
	}

	cfg := client.WorkspaceConfig()
	if cfg == nil {
		return fmt.Errorf("workspace %q has no configuration", client.Workspace())
	}

	instanceStatus, err := client.Status(ctx, client2.StatusOptions{ContainerStatus: true})
	if err != nil {
		return err
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}

	return render(mode, cfg, string(instanceStatus))
}

// render writes the workspace details to stdout in the resolved output mode.
func render(mode string, cfg *provider.Workspace, state string) error {
	switch mode {
	case output.ModePlain:
		table.Print([]string{"Field", "Value"}, describeRows(cfg, state))
	case output.ModeJSON:
		out, err := json.Marshal(&describeOutput{Workspace: cfg, State: state})
		if err != nil {
			return err
		}
		_, _ = fmt.Fprint(os.Stdout, string(out))
	}

	return nil
}

// validArgs provides shell completion suggestions for workspace arguments.
func (cmd *DescribeCmd) validArgs(
	rootCmd *cobra.Command,
	args []string,
	toComplete string,
) ([]string, cobra.ShellCompDirective) {
	return completion.GetWorkspaceSuggestions(
		rootCmd,
		cmd.Context,
		cmd.Provider,
		args,
		toComplete,
		cmd.Owner,
	)
}

// describeRows builds the curated Field/Value rows for the plain view,
// omitting rows whose value is empty.
func describeRows(cfg *provider.Workspace, state string) [][]string {
	rows := [][]string{}
	add := func(field, value string) {
		if value != "" {
			rows = append(rows, []string{field, value})
		}
	}

	add("ID", cfg.ID)
	add("State", state)
	add("Provider", cfg.Provider.Name)
	add("IDE", cfg.IDE.Name)
	add("Source", describeSource(cfg.Source))

	machine := cfg.Machine.ID
	if machine != "" && cfg.Machine.AutoDelete {
		machine += " (auto-delete: true)"
	}
	add("Machine", machine)

	add("Context", cfg.Context)
	if !cfg.CreationTimestamp.IsZero() {
		add("Created", time.Since(cfg.CreationTimestamp.Time).Round(time.Second).String())
	}
	if !cfg.LastUsedTimestamp.IsZero() {
		add("Last Used", time.Since(cfg.LastUsedTimestamp.Time).Round(time.Second).String())
	}

	return rows
}

func (cmd *DescribeCmd) execute(ctx context.Context, args []string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}
	client, err := workspace2.Get(ctx, workspace2.GetOptions{
		DevsyConfig:    devsyConfig,
		Args:           args,
		Owner:          cmd.Owner,
		ChangeLastUsed: false,
	})
	if err != nil {
		return err
	}
	return cmd.Run(ctx, client)
}

// describeSource condenses a WorkspaceSource into a single human-readable line,
// picking the populated variant: git, then local folder, image, or container.
func describeSource(src provider.WorkspaceSource) string {
	switch {
	case src.GitRepository != "":
		return describeGitSource(src)
	case src.LocalFolder != "":
		return src.LocalFolder
	case src.Image != "":
		return src.Image
	case src.Container != "":
		return src.Container
	default:
		return ""
	}
}

// describeGitSource renders the git variant of a WorkspaceSource, choosing the
// most specific ref (branch, then commit, then PR) and appending any subpath.
func describeGitSource(src provider.WorkspaceSource) string {
	out := provider.WorkspaceSourceGit + src.GitRepository
	switch {
	case src.GitBranch != "":
		out += "@" + src.GitBranch
	case src.GitCommit != "":
		out += "@" + src.GitCommit
	case src.GitPRReference != "":
		out += "@" + src.GitPRReference
	}
	if src.GitSubPath != "" {
		out += fmt.Sprintf(" (%s)", src.GitSubPath)
	}
	return out
}
