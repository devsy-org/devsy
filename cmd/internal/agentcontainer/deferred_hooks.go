//go:build !windows

package agentcontainer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/compress"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/setup"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

// DeferredHooksCmd runs deferred lifecycle hooks as a detached background process.
type DeferredHooksCmd struct {
	*flags.GlobalFlags
	SetupInfo      string
	Prebuild       bool
	DotfilesRepo   string
	DotfilesScript string
	SecretsEnv     []string
}

// NewDeferredHooksCmd creates a new command.
func NewDeferredHooksCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &DeferredHooksCmd{
		GlobalFlags: flags,
	}
	deferredCmd := &cobra.Command{
		Use:   "deferred-hooks",
		Short: "Runs deferred lifecycle hooks (phases after waitFor)",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	cliflags.Add(
		deferredCmd,
		cliflags.String(&cmd.SetupInfo, names.SetupInfo, "", "The container setup info"),
		cliflags.Bool(&cmd.Prebuild, names.Prebuild, false, "If true, prebuild lifecycle mode"),
		cliflags.String(&cmd.DotfilesRepo, names.DotfilesRepo, "", "Dotfiles repository URL"),
		cliflags.String(
			&cmd.DotfilesScript,
			names.DotfilesScript,
			"",
			"Dotfiles install script path",
		),
		cliflags.StringSlice(
			&cmd.SecretsEnv,
			names.SecretsEnv,
			[]string{},
			"Secrets to inject into lifecycle commands (KEY=VALUE)",
		),
	)
	_ = deferredCmd.MarkFlagRequired(names.SetupInfo)
	return deferredCmd
}

// Run executes the deferred lifecycle hooks.
func (cmd *DeferredHooksCmd) Run(ctx context.Context) error {
	decompressed, err := compress.Decompress(cmd.SetupInfo)
	if err != nil {
		return err
	}

	setupInfo := &config.Result{}
	if err := json.Unmarshal([]byte(decompressed), setupInfo); err != nil {
		return err
	}

	log.Debugf("running deferred lifecycle hooks")
	deferred, err := setup.RunPreAttachHooks(ctx, setupInfo, cmd.Prebuild, setup.DotfilesConfig{
		Repository:    cmd.DotfilesRepo,
		InstallScript: cmd.DotfilesScript,
		RemoteUser:    config.GetRemoteUser(setupInfo),
	}, cmd.SecretsEnv, setup.SkipPhases{})
	if err != nil {
		return fmt.Errorf("deferred hooks setup: %w", err)
	}

	if err := deferred.Run(); err != nil {
		return fmt.Errorf("deferred lifecycle hooks: %w", err)
	}

	return nil
}
