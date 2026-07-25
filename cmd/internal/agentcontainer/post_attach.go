//go:build !windows

package agentcontainer

import (
	"context"
	"encoding/json"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/compress"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/setup"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

// PostAttachCmd runs postAttachCommand hooks as a detached background process.
type PostAttachCmd struct {
	*flags.GlobalFlags
	SetupInfo string
}

// NewPostAttachCmd creates a new command.
func NewPostAttachCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &PostAttachCmd{
		GlobalFlags: flags,
	}
	postAttachCmd := &cobra.Command{
		Use:   "post-attach",
		Short: "Runs postAttachCommand lifecycle hooks",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	cliflags.Add(
		postAttachCmd,
		cliflags.String(&cmd.SetupInfo, names.SetupInfo, "", "The container setup info"),
	)
	_ = postAttachCmd.MarkFlagRequired(names.SetupInfo)
	return postAttachCmd
}

// Run runs the postAttachCommand lifecycle hooks.
func (cmd *PostAttachCmd) Run(ctx context.Context) error {
	decompressed, err := compress.Decompress(cmd.SetupInfo)
	if err != nil {
		return err
	}

	setupInfo := &config.Result{}
	if err := json.Unmarshal([]byte(decompressed), setupInfo); err != nil {
		return err
	}

	log.Debugf("running postAttachCommand hooks")
	// Mount values aren't in this process's env; read them back for redaction.
	if err := setup.RunPostAttachHooks(
		ctx,
		setupInfo,
		secretsEnvFromEnvironment(),
		setup.MountSecretsForRedaction(),
	); err != nil {
		log.Errorf("postAttachCommand failed: %v", err)
	}

	return nil
}
