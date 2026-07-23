package cmdinternal

import (
	"context"
	"fmt"
	"os"

	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/shell"
	"github.com/spf13/cobra"
)

type ShellCommand struct {
	Command string
	Login   bool
}

// NewShellCmd creates a new command.
func NewShellCmd() *cobra.Command {
	cmd := &ShellCommand{}
	shellCmd := &cobra.Command{
		Use:   "sh",
		Short: "Executes a command in a shell",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	cliflags.Add(
		shellCmd,
		cliflags.Bool(&cmd.Login, names.Login, false, "If login shell should be used").
			Shorthand("l"),
		cliflags.String(&cmd.Command, names.Command, "", "Command to execute").Shorthand("c"),
	)
	return shellCmd
}

func (cmd *ShellCommand) Run(ctx context.Context, args []string) error {
	switch {
	case cmd.Command == "" && len(args) == 0:
		return nil
	case cmd.Command != "" && len(args) > 0:
		return fmt.Errorf("either use -c or provide a script file")
	case len(args) > 1:
		return fmt.Errorf("only a single script file can be used")
	}

	// load command from file
	if len(args) > 0 {
		content, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}

		cmd.Command = string(content)
	}

	return shell.RunEmulatedShell(ctx, cmd.Command, os.Stdin, os.Stdout, os.Stderr, os.Environ())
}
