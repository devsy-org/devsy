package mcp

import (
	"context"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

// ServeCmd holds configuration for `devsy mcp serve`.
type ServeCmd struct {
	*flags.GlobalFlags

	ExecTimeoutDefault time.Duration
	ExecTimeoutMax     time.Duration
	ExecOutputCap      int
}

// NewServeCmd builds the `serve` subcommand.
func NewServeCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &ServeCmd{GlobalFlags: globalFlags}
	cobraCmd := &cobra.Command{
		Use:   "serve",
		Short: "Run an MCP server over stdio",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	cobraCmd.Flags().DurationVar(&cmd.ExecTimeoutDefault, "exec-timeout-default", 5*time.Minute,
		"Default timeout for workspace_exec calls")
	cobraCmd.Flags().DurationVar(&cmd.ExecTimeoutMax, "exec-timeout-max", 30*time.Minute,
		"Maximum timeout for workspace_exec calls (caller values are clamped)")
	cobraCmd.Flags().IntVar(&cmd.ExecOutputCap, "exec-output-cap", 100*1024,
		"Per-stream byte cap for workspace_exec output; excess is replaced with a truncation marker")
	return cobraCmd
}

// Run wires up the MCP server and serves over stdio until ctx is cancelled.
func (cmd *ServeCmd) Run(ctx context.Context) error {
	log.Debugf("starting MCP server (timeout default=%s max=%s cap=%dB)",
		cmd.ExecTimeoutDefault, cmd.ExecTimeoutMax, cmd.ExecOutputCap)
	// Tool registration and stdio loop are added in later tasks.
	_ = ctx
	return nil
}
