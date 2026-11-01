package cmd

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/flatpak"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

type passiveInvocationKey struct{}

// Cobra's Find retains the deepest command even when legacy argument validation
// fails. Its flag metadata must include help before resolving --help paths.
func resolveInvocation(root *cobra.Command, args []string) (*cobra.Command, bool) {
	target, remaining := resolveInvocationCommand(root, args)
	return target, passiveCommand(target, remaining)
}

func resolveInvocationCommand(root *cobra.Command, args []string) (*cobra.Command, []string) {
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd(args...)
	initHelpFlags(root)
	target, remaining, _ := root.Find(args)
	if target == nil {
		target = root
	}
	return target, remaining
}

func initHelpFlags(command *cobra.Command) {
	command.InitDefaultHelpFlag()
	for _, child := range command.Commands() {
		initHelpFlags(child)
	}
}

func passiveCommand(command *cobra.Command, args []string) bool {
	root := command.Root()
	help, _, _ := root.Find([]string{"help"})
	if command == help {
		target, _, _ := root.Find(args)
		return flags.IsPassive(target)
	}
	return flags.IsPassive(command)
}

func passiveInvocation(command *cobra.Command, args []string) bool {
	if ctx := command.Context(); ctx != nil {
		if passive, ok := ctx.Value(passiveInvocationKey{}).(bool); ok {
			return passive
		}
	}
	return passiveCommand(command, args)
}

func withInvocationPolicy(ctx context.Context, passive bool) context.Context {
	return context.WithValue(ctx, passiveInvocationKey{}, passive)
}

func passiveHostInspection(inSandbox bool) error {
	if inSandbox {
		return fmt.Errorf(
			"passive host inspection is unsupported inside Flatpak; run the host Devsy CLI",
		)
	}
	return nil
}

func preparePassive(command *cobra.Command, globalFlags *flags.GlobalFlags) (string, error) {
	if err := passiveHostInspection(flatpak.InSandbox()); err != nil {
		return "", err
	}
	paths := config.NewReadOnlyPathManagerForHome(globalFlags.DevsyHome)
	if err := config.CheckPendingContextDeletionWithPathManager(paths); err != nil {
		return "", err
	}
	snapshot, err := config.LoadConfigWithPathManager(
		paths,
		globalFlags.Context,
		globalFlags.Provider,
	)
	if err != nil {
		return "", err
	}
	command.SetContext(flags.WithPassiveInputs(command.Context(), flags.PassiveInputs{
		Paths: paths, Config: snapshot,
	}))
	level := snapshot.ContextOption(config.ContextOptionLogLevel)
	if level == "" {
		level = log.DefaultLevel
	}
	return level, nil
}
