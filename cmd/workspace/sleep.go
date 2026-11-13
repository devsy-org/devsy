package workspace

import (
	"io"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

// lifecycleCmd holds `devsy workspace sleep` and `devsy workspace wake`.
type lifecycleCmd struct {
	*flags.GlobalFlags

	action lifecycleAction

	// Out receives the result. Nil selects stdout.
	Out io.Writer
}

func newLifecycleCobraCmd(
	globalFlags *flags.GlobalFlags,
	action lifecycleAction,
	cobraCmd *cobra.Command,
) *cobra.Command {
	cmd := &lifecycleCmd{GlobalFlags: globalFlags, action: action}
	cobraCmd.Args = cobra.ExactArgs(1)
	cobraCmd.RunE = func(c *cobra.Command, args []string) error {
		cmd.Out = c.OutOrStdout()
		return cmd.Run(c, args[0])
	}
	return cobraCmd
}

// Run sends the request and returns once the gateway has accepted it.
func (cmd *lifecycleCmd) Run(c *cobra.Command, name string) error {
	target, err := resolveLifecycleTarget(cmd.GlobalFlags, cmd.Out)
	if err != nil {
		return err
	}
	return runPlatformLifecycle(c.Context(), target, cmd.action, []string{name})
}

// NewSleepCmd creates the `devsy workspace sleep` command, which asks the
// platform gateway to put a workspace to sleep.
func NewSleepCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	return newLifecycleCobraCmd(globalFlags, sleepAction, &cobra.Command{
		Use:   "sleep NAME",
		Short: "Put a platform workspace to sleep",
		Long: "Put a workspace on the platform gateway you are logged in to to sleep.\n\n" +
			"The gateway accepts the request and the workspace scales down in the " +
			"background, so it is not asleep when this command returns. " +
			"Run 'devsy workspace list' to follow its phase.",
		Example: "  devsy workspace sleep my-workspace",
	})
}

// NewWakeCmd creates the `devsy workspace wake` command, which asks the
// platform gateway to wake a sleeping workspace.
func NewWakeCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	return newLifecycleCobraCmd(globalFlags, wakeAction, &cobra.Command{
		Use:   "wake NAME",
		Short: "Wake a sleeping platform workspace",
		Long: "Wake a sleeping workspace on the platform gateway you are logged in to.\n\n" +
			"The gateway accepts the request and the workspace starts in the " +
			"background, so it is not running when this command returns. " +
			"Run 'devsy workspace list' to follow its phase.",
		Example: "  devsy workspace wake my-workspace",
	})
}
