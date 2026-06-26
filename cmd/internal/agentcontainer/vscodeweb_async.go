package agentcontainer

import (
	"encoding/json"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/compress"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/ide/vscodeweb"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

// VSCodeWebAsyncCmd holds the cmd flags.
type VSCodeWebAsyncCmd struct {
	*flags.GlobalFlags

	SetupInfo string
}

// NewVSCodeWebAsyncCmd creates a new command.
func NewVSCodeWebAsyncCmd() *cobra.Command {
	cmd := &VSCodeWebAsyncCmd{}
	vsCodeWebAsyncCmd := &cobra.Command{
		Use:   "vscode-web-async",
		Short: "Starts VS Code Web",
		Args:  cobra.NoArgs,
		RunE:  cmd.Run,
	}
	vsCodeWebAsyncCmd.Flags().
		StringVar(&cmd.SetupInfo, "setup-info", "", "The container setup info")
	_ = vsCodeWebAsyncCmd.MarkFlagRequired("setup-info")
	return vsCodeWebAsyncCmd
}

// Run runs the command logic.
func (cmd *VSCodeWebAsyncCmd) Run(_ *cobra.Command, _ []string) error {
	log.Debugf("Start setting up container")
	decompressed, err := compress.Decompress(cmd.SetupInfo)
	if err != nil {
		return err
	}

	setupInfo := &config.Result{}
	if err := json.Unmarshal([]byte(decompressed), setupInfo); err != nil {
		return err
	}

	return setupVSCodeWebExtensions(setupInfo)
}

func setupVSCodeWebExtensions(setupInfo *config.Result) error {
	vsCodeConfiguration := config.GetVSCodeConfiguration(setupInfo.MergedConfig)
	user := config.GetRemoteUser(setupInfo)
	return vscodeweb.NewVSCodeWeb(vscodeweb.ServerOptions{
		Extensions: vsCodeConfiguration.Extensions,
		UserName:   user,
	}).InstallExtensions()
}
