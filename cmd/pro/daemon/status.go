package daemon

import (
	"context"
	"encoding/json"
	"fmt"

	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/cmd/pro/proutil"
	"github.com/devsy-org/devsy/pkg/config"
	platformdaemon "github.com/devsy-org/devsy/pkg/daemon/platform"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

// StatusCmd holds the Devsy daemon flags.
type StatusCmd struct {
	*proflags.GlobalFlags

	Host string
}

// NewStatusCmd creates a new command.
func NewStatusCmd(flags *proflags.GlobalFlags) *cobra.Command {
	cmd := &StatusCmd{
		GlobalFlags: flags,
	}
	c := &cobra.Command{
		Use:              "status",
		Short:            "Get the status of the daemon",
		RunE:             proutil.HostRunE(flags, &cmd.Host, cmd.Run),
		PersistentPreRun: proutil.MarkAgentExecuted,
	}
	proutil.AddHostFlag(c, &cmd.Host, proutil.WithHostCompletion(flags))

	return c
}

func (cmd *StatusCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *providerpkg.ProviderConfig,
) error {
	status, err := platformdaemon.NewLocalClient(provider.Name).Status(ctx, cmd.Debug)
	if err != nil {
		return err
	}
	out, err := json.Marshal(status)
	if err != nil {
		return err
	}

	fmt.Print(string(out))

	return nil
}
