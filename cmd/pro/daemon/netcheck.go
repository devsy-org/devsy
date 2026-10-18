package daemon

import (
	"context"
	"fmt"
	"strconv"

	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/cmd/pro/proutil"
	"github.com/devsy-org/devsy/pkg/config"
	daemon "github.com/devsy-org/devsy/pkg/daemon/platform"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/spf13/cobra"
	"tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"
)

// NetcheckCmd holds the Devsy daemon flags.
type NetcheckCmd struct {
	*proflags.GlobalFlags

	Host string
}

// NewNetcheckCmd creates a new command.
func NewNetcheckCmd(flags *proflags.GlobalFlags) *cobra.Command {
	cmd := &NetcheckCmd{
		GlobalFlags: flags,
	}
	c := &cobra.Command{
		Use:              "netcheck",
		Short:            "Get the status of the current network",
		RunE:             proutil.HostRunE(flags, &cmd.Host, cmd.Run),
		PersistentPreRun: proutil.MarkAgentExecuted,
	}
	proutil.AddHostFlag(c, &cmd.Host, proutil.WithHostCompletion(flags))

	return c
}

func (cmd *NetcheckCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *providerpkg.ProviderConfig,
) error {
	tsClient := &local.Client{
		Socket:        daemon.GetSocketAddr(provider.Name),
		UseSocketOnly: true,
	}

	dm, err := tsClient.CurrentDERPMap(ctx)
	if err != nil {
		return err
	}

	rows := [][]string{}
	for _, region := range dm.Regions {
		report, err := tsClient.DebugDERPRegion(ctx, strconv.Itoa(region.RegionID))
		if err != nil {
			return err
		}
		regionLabel := fmt.Sprintf("DERP %d (%s)", region.RegionID, region.RegionCode)
		rows = append(rows, derpRegionRows(report, regionLabel)...)
	}

	table.Print([]string{"Region", "Level", "Message"}, rows)

	return nil
}

func derpRegionRows(report *ipnstate.DebugDERPRegionReport, regionLabel string) [][]string {
	rows := [][]string{}
	for _, e := range report.Errors {
		rows = append(rows, []string{regionLabel, "Error", e})
	}
	for _, w := range report.Warnings {
		rows = append(rows, []string{regionLabel, "Warning", w})
	}
	for _, i := range report.Info {
		rows = append(rows, []string{regionLabel, "Info", i})
	}
	if len(report.Errors) == 0 && len(report.Warnings) == 0 && len(report.Info) == 0 {
		rows = append(rows, []string{regionLabel, "", ""})
	}

	return rows
}
