package container

import (
	"encoding/json"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/compress"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/ide/vscode"
	oldlog "github.com/devsy-org/log"
	"github.com/spf13/cobra"
)

// VSCodeAsyncCmd holds the cmd flags.
type VSCodeAsyncCmd struct {
	*flags.GlobalFlags

	SetupInfo string
	Flavor    string
}

// NewVSCodeAsyncCmd creates a new command.
func NewVSCodeAsyncCmd() *cobra.Command {
	cmd := &VSCodeAsyncCmd{}
	vsCodeAsyncCmd := &cobra.Command{
		Use:   "vscode-async",
		Short: "Starts vscode",
		Args:  cobra.NoArgs,
		RunE:  cmd.Run,
	}
	vsCodeAsyncCmd.Flags().StringVar(&cmd.SetupInfo, "setup-info", "", "The container setup info")
	_ = vsCodeAsyncCmd.MarkFlagRequired("setup-info")

	vsCodeAsyncCmd.Flags().
		StringVar(&cmd.Flavor, "flavor", string(vscode.FlavorStable), "The flavor of the VSCode distribution")
	return vsCodeAsyncCmd
}

// Run runs the command logic.
func (cmd *VSCodeAsyncCmd) Run(_ *cobra.Command, _ []string) error {
	decompressed, err := compress.Decompress(cmd.SetupInfo)
	if err != nil {
		return err
	}

	setupInfo := &config.Result{}
	err = json.Unmarshal([]byte(decompressed), setupInfo)
	if err != nil {
		return err
	}

	err = setupVSCodeExtensions(setupInfo, vscode.Flavor(cmd.Flavor), oldlog.Default)
	if err != nil {
		return err
	}

	return nil
}

func setupVSCodeExtensions(
	setupInfo *config.Result,
	flavor vscode.Flavor,
	logger oldlog.Logger,
) error {
	vsCodeConfiguration := config.GetVSCodeConfiguration(setupInfo.MergedConfig)
	user := config.GetRemoteUser(setupInfo)
	return vscode.NewVSCodeServer(vscode.ServerOptions{
		Extensions: vsCodeConfiguration.Extensions,
		UserName:   user,
		Flavor:     flavor,
		Log:        logger,
	}).InstallExtensions()
}
