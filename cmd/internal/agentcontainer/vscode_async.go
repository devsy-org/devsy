package agentcontainer

import (
	"encoding/json"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/compress"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/ide/vscode"
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
	cliflags.Add(
		vsCodeAsyncCmd,
		cliflags.String(&cmd.SetupInfo, names.SetupInfo, "", "The container setup info"),
	)
	_ = vsCodeAsyncCmd.MarkFlagRequired(names.SetupInfo)

	cliflags.Add(
		vsCodeAsyncCmd,
		cliflags.String(
			&cmd.Flavor,
			names.Flavor,
			string(vscode.FlavorStable),
			"The flavor of the VSCode distribution",
		),
	)
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

	err = setupVSCodeExtensions(setupInfo, vscode.Flavor(cmd.Flavor))
	if err != nil {
		return err
	}

	return nil
}

func setupVSCodeExtensions(
	setupInfo *config.Result,
	flavor vscode.Flavor,
) error {
	vsCodeConfiguration := config.GetVSCodeConfiguration(setupInfo.MergedConfig)
	user := config.GetRemoteUser(setupInfo)
	return vscode.NewVSCodeServer(vscode.ServerOptions{
		Extensions: vsCodeConfiguration.Extensions,
		UserName:   user,
		Flavor:     flavor,
	}).InstallExtensions()
}
