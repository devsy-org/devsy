package container

import (
	"encoding/json"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/compress"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/ide/codeserver"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

// CodeServerAsyncCmd holds the cmd flags.
type CodeServerAsyncCmd struct {
	*flags.GlobalFlags

	SetupInfo string
}

// NewCodeServerAsyncCmd creates a new command.
func NewCodeServerAsyncCmd() *cobra.Command {
	cmd := &CodeServerAsyncCmd{}
	codeServerAsyncCmd := &cobra.Command{
		Use:   "code-server-async",
		Short: "Starts code-server",
		Args:  cobra.NoArgs,
		RunE:  cmd.Run,
	}
	codeServerAsyncCmd.Flags().
		StringVar(&cmd.SetupInfo, "setup-info", "", "The container setup info")
	_ = codeServerAsyncCmd.MarkFlagRequired("setup-info")
	return codeServerAsyncCmd
}

// Run runs the command logic.
func (cmd *CodeServerAsyncCmd) Run(_ *cobra.Command, _ []string) error {
	log.Debugf("Start setting up container")
	decompressed, err := compress.Decompress(cmd.SetupInfo)
	if err != nil {
		return err
	}

	setupInfo := &config.Result{}
	if err := json.Unmarshal([]byte(decompressed), setupInfo); err != nil {
		return err
	}

	return setupCodeServerExtensions(setupInfo)
}

func setupCodeServerExtensions(setupInfo *config.Result) error {
	vsCodeConfiguration := config.GetVSCodeConfiguration(setupInfo.MergedConfig)
	user := config.GetRemoteUser(setupInfo)
	return codeserver.NewCodeServer(codeserver.ServerOptions{
		Extensions: vsCodeConfiguration.Extensions,
		UserName:   user,
	}).InstallExtensions()
}
