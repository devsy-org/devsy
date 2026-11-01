package pro

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	platformflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// ListCmd holds the list cmd flags.
type ListCmd struct {
	platformflags.GlobalFlags

	Login bool
}

// NewListCmd creates a new command.
func NewListCmd(flags *platformflags.GlobalFlags) *cobra.Command {
	cmd := &ListCmd{
		GlobalFlags: *flags,
	}
	listCmd := &cobra.Command{
		Use:         "list",
		Aliases:     []string{"ls"},
		Short:       "List available Devsy Platform instances",
		Args:        cobra.NoArgs,
		Annotations: telemetry.SkipInUIAnnotation(),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	cliflags.Add(
		listCmd,
		cliflags.Bool(
			&cmd.Login,
			names.Login,
			false,
			"Check if the user is logged into the platform instance",
		),
	)
	return listCmd
}

// Run runs the command logic.
func (cmd *ListCmd) Run(ctx context.Context) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	platformInstances, err := workspace.ListPlatformInstances(devsyConfig)
	if err != nil {
		return err
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	switch mode {
	case output.ModePlain:
		cmd.printPlain(ctx, devsyConfig, platformInstances)
	case output.ModeJSON:
		return cmd.printJSON(ctx, devsyConfig, platformInstances)
	}

	return nil
}

func (cmd *ListCmd) printPlain(
	ctx context.Context,
	devsyConfig *config.Config,
	platformInstances []*provider.PlatformInstance,
) {
	tableEntries := [][]string{}
	for _, platformInstance := range platformInstances {
		entry := []string{
			platformInstance.Host,
			platformInstance.Provider,
			time.Since(platformInstance.CreationTimestamp.Time).Round(1 * time.Second).String(),
		}
		if cmd.Login {
			err := checkLogin(ctx, devsyConfig, platformInstance)
			entry = append(entry, fmt.Sprintf("%t", err == nil))
		}

		tableEntries = append(tableEntries, entry)
	}
	sort.SliceStable(tableEntries, func(i, j int) bool {
		return tableEntries[i][0] < tableEntries[j][0]
	})

	tableHeaders := []string{
		"Host",
		"Provider",
		"Age",
	}
	if cmd.Login {
		tableHeaders = append(tableHeaders, "Authenticated")
	}

	table.Print(tableHeaders, tableEntries)
}

func (cmd *ListCmd) printJSON(
	ctx context.Context,
	devsyConfig *config.Config,
	platformInstances []*provider.PlatformInstance,
) error {
	tableEntries := []*platformTableEntry{}
	for _, platformInstance := range platformInstances {
		entry := &platformTableEntry{
			PlatformInstance: platformInstance,
			Context:          devsyConfig.DefaultContext,
			Capabilities:     getCapabilities(devsyConfig, platformInstance),
		}
		if cmd.Login {
			err := checkLogin(ctx, devsyConfig, platformInstance)
			isAuthenticated := err == nil
			entry.Authenticated = &isAuthenticated
		}

		tableEntries = append(tableEntries, entry)
	}

	sort.SliceStable(tableEntries, func(i, j int) bool {
		return tableEntries[i].Host < tableEntries[j].Host
	})
	out, err := json.Marshal(tableEntries)
	if err != nil {
		return err
	}
	fmt.Print(string(out)) //nolint:forbidigo // CLI stdout output

	return nil
}

type platformTableEntry struct {
	*provider.PlatformInstance

	Authenticated *bool        `json:"authenticated,omitempty"`
	Context       string       `json:"context,omitempty"`
	Capabilities  []Capability `json:"capabilities,omitempty"`
}

type Capability string

var (
	capabilityDaemon         Capability = "daemon"
	capabilityHealthCheck    Capability = "health-check"
	capabilityUpdateProvider Capability = "update-provider"
)

func checkLogin(
	ctx context.Context,
	devsyConfig *config.Config,
	platformInstance *provider.PlatformInstance,
) error {
	// for every platform instance, check auth status by calling login
	if err := login(
		ctx,
		devsyConfig,
		platformInstance.Host,
		platformInstance.Provider,
		"",
		true,
		false,
	); err != nil {
		return fmt.Errorf("not logged into %s", platformInstance.Host)
	}

	return nil
}

func getCapabilities(
	devsyConfig *config.Config,
	platformInstance *provider.PlatformInstance,
) []Capability {
	capabilities := []Capability{}
	provider, err := workspace.FindProvider(devsyConfig, platformInstance.Provider)
	if err != nil {
		return capabilities
	}

	if provider.Config.HasHealthCheck() {
		capabilities = append(capabilities, capabilityHealthCheck)
		capabilities = append(capabilities, capabilityUpdateProvider)
	}

	if provider.Config.IsDaemonProvider() {
		capabilities = append(capabilities, capabilityDaemon)
	}

	return capabilities
}
