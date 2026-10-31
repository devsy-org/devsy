package agentworkspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	"github.com/devsy-org/devsy/pkg/driver"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/spf13/cobra"
)

const (
	sourceContainer = "container"
	sourceSetup     = "setup"

	// containerRootUser owns the container spool.
	containerRootUser = "root"
	// maxRemoteStderr bounds the diagnostics kept from the container reader.
	maxRemoteStderr = 4 * 1024
)

// LogsCmd holds the cmd flags.
type LogsCmd struct {
	*flags.GlobalFlags

	ID string

	Source     string
	RunID      string
	Hook       string
	Invocation string
	After      string
	Limit      int
}

// NewLogsCmd creates a new command.
func NewLogsCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &LogsCmd{
		GlobalFlags: flags,
	}
	c := &cobra.Command{
		Use:   "logs",
		Short: "Returns the workspace container logs",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	cliflags.Add(c, cliflags.String(&cmd.ID, names.ID, "", "The workspace id"))
	_ = c.MarkFlagRequired(names.ID)
	c.Flags().StringVar(&cmd.Source, names.Source, sourceContainer, "container or setup")
	c.Flags().StringVar(&cmd.RunID, "run", "", "The run ID (setup source)")
	c.Flags().StringVar(&cmd.Hook, "hook", "", "Only this lifecycle hook (setup source)")
	c.Flags().StringVar(&cmd.Invocation, "invocation", "", "Only this invocation ID (setup source)")
	c.Flags().StringVar(&cmd.After, "after", "", "Cursor of the previous page (setup source)")
	c.Flags().IntVar(&cmd.Limit, "limit", 0, "Most records in the page (setup source)")

	return c
}

func (cmd *LogsCmd) Run(ctx context.Context) error {
	if cmd.Source == sourceSetup {
		return cmd.runSetup(ctx)
	}

	shouldExit, workspaceInfo, err := agent.ReadAgentWorkspaceInfo(
		cmd.AgentDir,
		cmd.Context,
		cmd.ID,
	)
	if err != nil {
		return err
	} else if shouldExit {
		return nil
	}
	if workspaceInfo == nil {
		return fmt.Errorf("workspace %q not found", cmd.ID)
	}

	runner, err := devcontainer.NewRunner(
		ctx,
		config.ContainerDevsyHelperLocation,
		config.DefaultAgentDownloadURL(),
		workspaceInfo,
	)
	if err != nil {
		return fmt.Errorf("create runner: %w", err)
	}

	return runner.Logs(ctx, os.Stdout)
}

// runSetup relays one bounded page from the container spool. It only reads
// existing state: no agent info is provisioned, no elevation is requested and
// the runtime is asked to exec only, never to start anything.
func (cmd *LogsCmd) runSetup(ctx context.Context) error {
	read := workspacelogs.ReadRequest{
		RunID: cmd.RunID, Hook: cmd.Hook, InvocationID: cmd.Invocation,
		After: cmd.After, Limit: cmd.Limit,
	}
	if err := workspacelogs.ValidateReadSelectors(read); err != nil {
		return err
	}
	info, err := agent.ReadExistingAgentWorkspaceInfo(cmd.AgentDir, cmd.Context, cmd.ID)
	if err != nil {
		return unavailable(os.Stderr, workspacelogs.ReasonRefreshFailed, err)
	}
	if cmd.UID != "" && cmd.UID != info.Workspace.UID {
		return fmt.Errorf("workspace %q does not match the requested workspace UID", cmd.ID)
	}
	runner, err := newReaderRunner(ctx, info)
	if err != nil {
		return unavailable(os.Stderr, workspacelogs.ReasonRefreshFailed, err)
	}

	relay := &setupRelay{
		runner: runner, info: info, read: read, stdout: os.Stdout, stderr: os.Stderr,
	}

	return relay.run(ctx)
}

// newReaderRunner builds a runner from a copy of the workspace info with
// auto-start disabled. The copy is never persisted.
func newReaderRunner(
	ctx context.Context,
	info *provider2.AgentWorkspaceInfo,
) (devcontainer.Runner, error) {
	reader := *info
	reader.CLIOptions.NoAutoStart = true

	return devcontainer.NewRunner(
		ctx,
		config.ContainerDevsyHelperLocation,
		config.DefaultAgentDownloadURL(),
		&reader,
	)
}

// setupRelay runs the container reader for one page and forwards its stdout.
type setupRelay struct {
	runner devcontainer.Runner
	info   *provider2.AgentWorkspaceInfo
	read   workspacelogs.ReadRequest
	stdout io.Writer
	stderr io.Writer
}

func (r *setupRelay) run(ctx context.Context) error {
	remoteErr := &workspacelogs.BoundedBuffer{Max: maxRemoteStderr}
	err := r.runner.Command(ctx, devcontainer.CommandParams{
		User:           containerRootUser,
		Command:        setupReaderCommand(r.info, r.read),
		Stdout:         r.stdout,
		Stderr:         remoteErr,
		RawStdout:      true,
		RequireRunning: true,
	})
	if err == nil {
		return nil
	}
	reason, ok := workspacelogs.ParseUnavailable(remoteErr.String())
	if !ok {
		reason = classifyReaderError(err)
		err = fmt.Errorf("%w: %s", err, remoteErr.String())
	}

	return unavailable(r.stderr, reason, err)
}

func classifyReaderError(err error) string {
	switch {
	case errors.Is(err, devcontainer.ErrRequireRunningUnsupported):
		return workspacelogs.ReasonRefreshUnsupported
	case errors.Is(err, driver.ErrNotRunning):
		return workspacelogs.ReasonSourceOffline
	default:
		return workspacelogs.ReasonRefreshFailed
	}
}

// setupReaderCommand builds the container command. Every value is validated
// and quoted; nothing the caller supplied is a path.
func setupReaderCommand(info *provider2.AgentWorkspaceInfo, read workspacelogs.ReadRequest) string {
	args := []string{
		shellescape.Quote(info.Agent.ContainerInstallPath()),
		"internal", "agent", "container", "setup-logs",
		"--context", shellescape.Quote(info.Workspace.Context),
		"--uid", shellescape.Quote(info.Workspace.UID),
		"--run", shellescape.Quote(read.RunID),
	}
	for _, opt := range []struct{ flag, value string }{
		{"--hook", read.Hook}, {"--invocation", read.InvocationID}, {"--after", read.After},
	} {
		if opt.value != "" {
			args = append(args, opt.flag, shellescape.Quote(opt.value))
		}
	}
	if read.Limit > 0 {
		args = append(args, "--limit", strconv.Itoa(read.Limit))
	}

	return strings.Join(args, " ")
}

// unavailable prints the typed reason for the caller and returns cause.
func unavailable(w io.Writer, reason string, cause error) error {
	_, _ = fmt.Fprint(w, workspacelogs.FormatUnavailable(reason))

	return cause
}
