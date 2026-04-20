package pro

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"

	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/platform/remotecommand"
	oldlog "github.com/devsy-org/log"
	"github.com/spf13/cobra"
)

// RebuildCmd holds the cmd flags.
type RebuildCmd struct {
	*flags.GlobalFlags

	Project string
	Host    string
}

// NewRebuildCmd creates a new command.
func NewRebuildCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &RebuildCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:   "rebuild",
		Short: "Rebuild a workspace",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	c.Flags().StringVar(&cmd.Project, "project", "", "The project to use")
	_ = c.MarkFlagRequired("project")
	c.Flags().StringVar(&cmd.Host, "host", "", "The pro instance to use")
	_ = c.MarkFlagRequired("host")

	return c
}

func (cmd *RebuildCmd) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("please provide a workspace name")
	}
	targetWorkspace := args[0]

	devsyConfig, err := config.LoadConfig(cmd.Context, "")
	if err != nil {
		return err
	}

	baseClient, err := platform.InitClientFromHost(ctx, devsyConfig, cmd.Host, oldlog.Default)
	if err != nil {
		return fmt.Errorf("resolve host \"%s\": %w", cmd.Host, err)
	}

	instanceOpts := platform.FindInstanceOptions{Name: targetWorkspace, ProjectName: cmd.Project}
	workspace, err := platform.FindInstance(ctx, baseClient, instanceOpts)
	if err != nil {
		return err
	}
	if workspace == nil {
		return fmt.Errorf("workspace %q not found in project %q", targetWorkspace, cmd.Project)
	}

	opts := struct {
		Recreate bool `json:"recreate"`
	}{Recreate: true}
	rawOpts, err := json.Marshal(opts)
	if err != nil {
		return err
	}
	values := url.Values{"options": []string{string(rawOpts)}, "cliMode": []string{"true"}}
	conn, err := platform.DialInstance(baseClient, workspace, "up", values, oldlog.Default)
	if err != nil {
		return err
	}

	_, err = remotecommand.ExecuteConn(
		ctx,
		conn,
		os.Stdin,
		os.Stdout,
		os.Stderr,
		oldlog.Default.ErrorStreamOnly(),
	)
	if err != nil {
		return fmt.Errorf("error executing: %w", err)
	}

	return nil
}
