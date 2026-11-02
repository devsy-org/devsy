package pro

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/pro/cmdutil"
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/telemetry"
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
		Use:              "check-update",
		Short:            "Check platform provider update",
		Hidden:           true,
		Annotations:      telemetry.SkipInUIAnnotation(),
		RunE:             cmdutil.HostRunE(globalFlags, &cmd.Host, cmd.Run),
		PersistentPreRun: cmdutil.MarkAgentExecuted,
	}
	cmdutil.AddHostFlag(c, &cmd.Host)

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

	_, _ = fmt.Fprint(os.Stdout, string(out))

	return nil
}
