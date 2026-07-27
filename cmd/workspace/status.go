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
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/provider"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// StatusCmd holds the cmd flags.
type StatusCmd struct {
	*flags.GlobalFlags
	client2.StatusOptions

	Timeout  string
	Recovery bool
}

// NewStatusCmd creates a new command.
func NewStatusCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &StatusCmd{
		GlobalFlags: globalFlags,
	}
	statusCmd := &cobra.Command{
		Use:   "status [flags] [workspace-path|workspace-name]",
		Short: "Show workspace status",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.execute(cobraCmd.Context(), args)
		},
		ValidArgsFunction: func(rootCmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return completion.GetWorkspaceSuggestions(
				rootCmd,
				cmd.Context,
				cmd.Provider,
				args,
				toComplete,
				cmd.Owner,
			)
		},
	}

	cliflags.Add(statusCmd,
		cliflags.Bool(&cmd.ContainerStatus, names.ContainerStatus, true,
			"If enabled shows the workspace container status as well"),
		cliflags.String(&cmd.Timeout, names.Timeout, "30s",
			"The timeout to wait until the status can be retrieved"),
		cliflags.Bool(&cmd.Recovery, names.Recovery, false,
			"Include whether the running container is a recovery container "+
				"(JSON output only)"),
	)
	return statusCmd
}

// Run runs the command logic.
func (cmd *StatusCmd) Run(
	ctx context.Context,
	client client2.BaseWorkspaceClient,
) error {
	// parse timeout
	if cmd.Timeout != "" {
		duration, err := time.ParseDuration(cmd.Timeout)
		if err != nil {
			return fmt.Errorf("parse --timeout: %w", err)
		}

		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, duration)
		defer cancel()
	}

	// get instance status
	instanceStatus, err := client.Status(ctx, cmd.StatusOptions)
	if err != nil {
		return err
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	switch mode {
	case output.ModePlain:
		_, _ = fmt.Fprintln(os.Stdout, string(instanceStatus))
	case output.ModeJSON:
		status := client2.WorkspaceStatus{
			ID:       client.Workspace(),
			Context:  client.Context(),
			Provider: client.Provider(),
			State:    string(instanceStatus),
			Recovery: cmd.resolveRecovery(client, instanceStatus),
		}
		out, err := json.Marshal(&status)
		if err != nil {
			return err
		}

		fmt.Print(string(out))
	}

	return nil
}

// resolveRecovery reports whether the running container was built in recovery
// mode, looked up from the persisted workspace result. It is opt-in (--recovery)
// so the frequent status poll pays no extra disk read.
func (cmd *StatusCmd) resolveRecovery(
	c client2.BaseWorkspaceClient,
	status client2.Status,
) bool {
	if !cmd.Recovery || status != client2.StatusRunning {
		return false
	}
	result, err := provider.LoadWorkspaceResult(c.Context(), c.Workspace())
	if err != nil || result == nil {
		return false
	}
	return result.RecoveryContainer
}

func (cmd *StatusCmd) execute(ctx context.Context, args []string) error {
	if _, err := clientimplementation.DecodeOptionsFromEnv(
		config.EnvFlagsStatus, &cmd.StatusOptions,
	); err != nil {
		return fmt.Errorf("decode status options: %w", err)
	}
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}
	client, err := workspace2.Get(ctx, workspace2.GetOptions{
		DevsyConfig: devsyConfig,
		Args:        args,
		Owner:       cmd.Owner,
	})
	if err != nil {
		return err
	}
	return cmd.Run(ctx, client)
}
