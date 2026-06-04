package mcp

import (
	"context"
	"os"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
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

	// Reserve the real stdout for the JSON-RPC frame and redirect os.Stdout to
	// stderr. Up.go's JSON envelopes are routed through an injected writer, but
	// other code paths in pkg/workspace still write directly to os.Stdout for
	// interactive prompts and progress. Belt-and-suspenders against any such
	// site corrupting the MCP stdio transport.
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
