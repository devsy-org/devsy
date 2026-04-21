package container

import (
	"encoding/json"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/compress"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/ide/openvscode"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

// OpenVSCodeAsyncCmd holds the cmd flags.
type OpenVSCodeAsyncCmd struct {
	*flags.GlobalFlags

	SetupInfo string
}

// NewOpenVSCodeAsyncCmd creates a new command.
func NewOpenVSCodeAsyncCmd() *cobra.Command {
	cmd := &OpenVSCodeAsyncCmd{}
	vsCodeAsyncCmd := &cobra.Command{
		Use:   "openvscode-async",
		Short: "Starts openvscode",
		Args:  cobra.NoArgs,
		RunE:  cmd.Run,
	}
	vsCodeAsyncCmd.Flags().StringVar(&cmd.SetupInfo, "setup-info", "", "The container setup info")
	_ = vsCodeAsyncCmd.MarkFlagRequired("setup-info")
	return vsCodeAsyncCmd
}

// Run runs the command logic.
func (cmd *OpenVSCodeAsyncCmd) Run(_ *cobra.Command, _ []string) error {
	log.Debugf("Start setting up container...")
	decompressed, err := compress.Decompress(cmd.SetupInfo)
	if err != nil {
		return err
	}

	setupInfo := &config.Result{}
	err = json.Unmarshal([]byte(decompressed), setupInfo)
	if err != nil {
		return err
	}

	// install IDE
	err = setupOpenVSCodeExtensions(setupInfo)
	if err != nil {
		return err
	}

	return nil
}

func setupOpenVSCodeExtensions(setupInfo *config.Result) error {
	vsCodeConfiguration := config.GetVSCodeConfiguration(setupInfo.MergedConfig)
	user := config.GetRemoteUser(setupInfo)
	return openvscode.NewOpenVSCodeServer(vsCodeConfiguration.Extensions, "", user, "", "", nil).
		InstallExtensions()
}
