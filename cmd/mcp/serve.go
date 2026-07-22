package mcp

import (
	"context"
	"os"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/version"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
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
	cliflags.Add(
		cobraCmd,
		cliflags.Duration(
			&cmd.ExecTimeoutDefault,
			names.ExecTimeoutDefault,
			5*time.Minute,
			"Default timeout for workspace_exec calls",
		),
		cliflags.Duration(
			&cmd.ExecTimeoutMax,
			names.ExecTimeoutMax,
			30*time.Minute,
			"Maximum timeout for workspace_exec calls (caller values are clamped)",
		),
		cliflags.Int(
			&cmd.ExecOutputCap,
			names.ExecOutputCap,
			100*1024,
			"Per-stream byte cap for workspace_exec output; excess is replaced with a truncation marker",
		),
	)
	return cobraCmd
}

// Run wires up the MCP server and serves over stdio until ctx is cancelled.
func (cmd *ServeCmd) Run(ctx context.Context) error {
	log.Debugf("starting MCP server (timeout default=%s max=%s cap=%dB)",
		cmd.ExecTimeoutDefault, cmd.ExecTimeoutMax, cmd.ExecOutputCap)

	// Reserve real stdout for the JSON-RPC frame; redirect os.Stdout to stderr
	// so any stray write elsewhere in the process can't corrupt the transport.
	realStdout := os.Stdout
	os.Stdout = os.Stderr
	defer func() { os.Stdout = realStdout }()

	transport := &sdkmcp.IOTransport{
		Reader: os.Stdin,
		Writer: realStdout,
	}

	server := sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    "devsy",
		Version: version.GetVersion(),
	}, nil)

	cmd.registerTools(server)

	return server.Run(ctx, transport)
}

func (cmd *ServeCmd) registerTools(s *sdkmcp.Server) {
	registerWorkspaceTools(s, cmd.GlobalFlags)
	registerExecTool(s, cmd)
	registerProviderTools(s, cmd.GlobalFlags)
}
