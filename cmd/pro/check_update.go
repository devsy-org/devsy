package pro

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/devsy-org/devsy/cmd/agent"
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/cmd/pro/proutil"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/provider"
	versionpkg "github.com/devsy-org/devsy/pkg/version"
	"github.com/spf13/cobra"
)

// CheckUpdateCmd holds the cmd flags.
type CheckUpdateCmd struct {
	*flags.GlobalFlags

	Host string
}

// NewCheckUpdateCmd creates a new command.
func NewCheckUpdateCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &CheckUpdateCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "check-update",
		Short:  "Check platform provider update",
		Hidden: true,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			devsyConfig, provider, err := proutil.FindProProvider(
				cobraCmd.Context(),
				cmd.Context,
				cmd.Provider,
				cmd.Host,
			)
			if err != nil {
				return err
			}

			return cmd.Run(cobraCmd.Context(), devsyConfig, provider)
		},
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			root := cmd.Root()
			if root == nil {
				return
			}
			if root.Annotations == nil {
				root.Annotations = map[string]string{}
			}
			// Don't print debug message
			root.Annotations[agent.AgentExecutedAnnotation] = "true"
		},
	}

	c.Flags().StringVar(&cmd.Host, "host", "", "The pro instance to use")
	_ = c.MarkFlagRequired("host")
	flags.BindEnv(c.Flags(), "host")

	return c
}

type ProviderUpdateInfo struct {
	Available  bool   `json:"available,omitempty"`
	NewVersion string `json:"newVersion,omitempty"`
}

func (cmd *CheckUpdateCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *provider.ProviderConfig,
) error {
	remoteVersion, err := platform.GetDevsyVersion(fmt.Sprintf("https://%s", cmd.Host))
	if err != nil {
		return err
	}

	providerUpdate := ProviderUpdateInfo{}
	if provider.Version == versionpkg.DevVersion {
		providerUpdate.Available = false
	} else if provider.Version != remoteVersion {
		providerUpdate.Available = true
		providerUpdate.NewVersion = remoteVersion
	}

	out, err := json.Marshal(providerUpdate)
	if err != nil {
		return err
	}

	fmt.Print(string(out))

	return nil
}
