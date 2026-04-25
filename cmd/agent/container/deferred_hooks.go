//go:build !windows

package container

import (
	"context"
	"encoding/json"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/compress"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/setup"
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
	deferredCmd.Flags().
		StringVar(&cmd.SetupInfo, "setup-info", "", "The container setup info")
	deferredCmd.Flags().
		BoolVar(&cmd.Prebuild, "prebuild", false, "If true, prebuild lifecycle mode")
	deferredCmd.Flags().
		StringVar(&cmd.DotfilesRepo, "dotfiles-repo", "", "Dotfiles repository URL")
	deferredCmd.Flags().
		StringVar(&cmd.DotfilesScript, "dotfiles-script", "", "Dotfiles install script path")
	_ = deferredCmd.MarkFlagRequired("setup-info")
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
	})
	if err != nil {
		log.Errorf("deferred hooks setup failed: %v", err)
		return nil
	}

	if err := deferred.Run(); err != nil {
		log.Errorf("deferred lifecycle hooks failed: %v", err)
	}

	return nil
}
