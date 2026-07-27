package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/provider"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	daemon "github.com/devsy-org/devsy/pkg/daemon/platform"
	"github.com/devsy-org/devsy/pkg/platform"
	pkgprovider "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/version"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type TroubleshootCmd struct {
	*flags.GlobalFlags
}

func NewTroubleshootCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &TroubleshootCmd{
		GlobalFlags: flags,
	}
	troubleshootCmd := &cobra.Command{
		Use:   "troubleshoot [workspace-path|workspace-name]",
		Short: "Print workspace troubleshooting information",
		Run: func(cobraCmd *cobra.Command, args []string) {
			cmd.Run(cobraCmd.Context(), args)
		},
		ValidArgsFunction: func(rootCmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return completion.GetWorkspaceSuggestions(
				rootCmd,
				cmd.Context,
				cmd.Provider,
				args,
				toComplete,
				cmd.Owner,
			)
		},
		Hidden: true,
	}

	return troubleshootCmd
}

type troubleshootInfo struct {
	CLIVersion            string
	Config                *config.Config
	Providers             map[string]provider.ProviderWithDefault
	DevsyProInstances     []DevsyProInstance
	Workspace             *pkgprovider.Workspace
	WorkspaceStatus       client.Status
	WorkspaceTroubleshoot *managementv1.DevsyWorkspaceInstanceTroubleshoot
	DaemonStatus          *daemon.Status

	Errors []PrintableError `json:",omitempty"`
}

func (info *troubleshootInfo) addErr(context string, err error) {
	info.Errors = append(info.Errors, PrintableError{fmt.Errorf("%s: %w", context, err)})
}

func printTroubleshootInfo(info *troubleshootInfo) {
	out, err := json.MarshalIndent(info, "", "  ")
	if err == nil {
		fmt.Print(string(out)) //nolint:forbidigo // CLI stdout output
	} else {
		fmt.Print(err)   //nolint:forbidigo // CLI stdout output
		fmt.Print(*info) //nolint:forbidigo // CLI stdout output
	}
}

func (cmd *TroubleshootCmd) Run(ctx context.Context, args []string) {
	info := &troubleshootInfo{CLIVersion: version.GetVersion()}

	// Print on every exit path, including panics.
	defer printTroubleshootInfo(info)

	// Collect as much as possible — partial info beats no info, so do not
	// return early on errors except where downstream steps require the result.
	var err error
	info.Config, err = config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		info.addErr("load config", err)
		// Without the devsy config no further troubleshooting is possible.
		return
	}

	info.Providers, err = collectProviders(info.Config)
	if err != nil {
		info.addErr("collect providers", err)
	}

	info.DevsyProInstances, err = collectPlatformInfo(info.Config)
	if err != nil {
		info.addErr("collect platform info", err)
	}

	cmd.collectWorkspaceInfo(ctx, info, args)
}

func (cmd *TroubleshootCmd) collectWorkspaceInfo(
	ctx context.Context,
	info *troubleshootInfo,
	args []string,
) {
	workspaceClient, err := workspace.Get(ctx, workspace.GetOptions{
		DevsyConfig: info.Config,
		Args:        args,
		Owner:       cmd.Owner,
	})
	if err != nil {
		info.addErr("get workspace", err)
		return
	}

	info.Workspace = workspaceClient.WorkspaceConfig()
	info.WorkspaceStatus, err = workspaceClient.Status(ctx, client.StatusOptions{})
	if err != nil {
		info.addErr("workspace status", err)
	}

	if info.Workspace.Pro != nil {
		info.WorkspaceTroubleshoot, err = collectWorkspaceProTroubleshoot(
			ctx, info.Config, info.Workspace, info.DevsyProInstances,
		)
		if err != nil {
			info.addErr("collect pro workspace info", err)
		}
	}

	info.DaemonStatus, err = collectDaemonStatus(ctx, workspaceClient)
	if err != nil {
		info.addErr("get daemon status", err)
	}
}

// collectWorkspaceProTroubleshoot locates the pro instance that owns the given
// workspace and retrieves its troubleshooting info. It returns (nil, nil) when
// no matching pro instance is configured.
func collectWorkspaceProTroubleshoot(
	ctx context.Context,
	devsyConfig *config.Config,
	ws *pkgprovider.Workspace,
	proInstances []DevsyProInstance,
) (*managementv1.DevsyWorkspaceInstanceTroubleshoot, error) {
	// Multiple pro instances may be configured; locate the one that owns this workspace.
	var proInstance DevsyProInstance
	for _, instance := range proInstances {
		if instance.ProviderName == ws.Provider.Name {
			proInstance = instance
			break
		}
	}

	if proInstance.ProviderName == "" {
		return nil, nil
	}

	return collectProWorkspaceInfo(ctx, devsyConfig, proInstance.Host, ws.UID, ws.Pro.Project)
}

// collectDaemonStatus returns the local daemon status when the client is a
// daemon client, or (nil, nil) otherwise.
func collectDaemonStatus(
	ctx context.Context,
	workspaceClient client.BaseWorkspaceClient,
) (*daemon.Status, error) {
	daemonClient, ok := workspaceClient.(client.DaemonClient)
	if !ok {
		return nil, nil
	}
	status, err := daemon.NewLocalClient(daemonClient.Provider()).Status(ctx, true)
	if err != nil {
		return nil, err
	}
	return &status, nil
}

// collectProWorkspaceInfo collects troubleshooting information for a Devsy Pro instance.
// It initializes a client from the host, finds the workspace instance in the project, and retrieves
// troubleshooting information using the management client.
func collectProWorkspaceInfo(
	ctx context.Context,
	devsyConfig *config.Config,
	host string,
	workspaceUID string,
	project string,
) (*managementv1.DevsyWorkspaceInstanceTroubleshoot, error) {
	baseClient, err := platform.InitClientFromHost(ctx, devsyConfig, host)
	if err != nil {
		return nil, fmt.Errorf("init client from host: %w", err)
	}

	opts := platform.FindInstanceOptions{UID: workspaceUID, ProjectName: project}
	workspace, err := platform.FindInstance(ctx, baseClient, opts)
	if err != nil {
		return nil, err
	} else if workspace == nil {
		return nil, fmt.Errorf("couldn't find workspace")
	}

	managementClient, err := baseClient.Management()
	if err != nil {
		return nil, fmt.Errorf("management: %w", err)
	}

	troubleshoot, err := managementClient.
		Loft().
		ManagementV1().
		DevsyWorkspaceInstances(workspace.Namespace).
		Troubleshoot(ctx, workspace.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("troubleshoot: %w", err)
	}

	return troubleshoot, nil
}

// collectProviders collects and configures providers based on the given devsyConfig.
// It returns a map of providers with their default settings and an error if any occurs.
func collectProviders(
	devsyConfig *config.Config,
) (map[string]provider.ProviderWithDefault, error) {
	providers, err := workspace.LoadAllProviders(devsyConfig)
	if err != nil {
		return nil, err
	}

	configuredProviders := devsyConfig.Current().Providers
	if configuredProviders == nil {
		configuredProviders = map[string]*config.ProviderConfig{}
	}

	retMap := map[string]provider.ProviderWithDefault{}
	for k, entry := range providers {
		if configuredProviders[entry.Config.Name] == nil {
			continue
		}

		srcOptions := provider.MergeDynamicOptions(
			entry.Config.Options,
			configuredProviders[entry.Config.Name].DynamicOptions,
		)
		entry.Config.Options = srcOptions
		retMap[k] = provider.ProviderWithDefault{
			ProviderWithOptions: *entry,
			Default:             devsyConfig.Current().DefaultProvider == entry.Config.Name,
		}
	}

	return retMap, nil
}

type DevsyProInstance struct {
	Host         string
	ProviderName string
	Version      string
}

// collectPlatformInfo collects information about all platform instances in a given devsyConfig.
// It iterates over the pro instances, retrieves their versions, and appends them to the ProInstance slice.
// Any errors encountered during this process are combined and returned along with the ProInstance slice.
// This means that even when an error value is returned, the pro instance slice will contain valid values.
func collectPlatformInfo(
	devsyConfig *config.Config,
) ([]DevsyProInstance, error) {
	proInstanceList, err := workspace.ListProInstances(devsyConfig)
	if err != nil {
		return nil, fmt.Errorf("list pro instances: %w", err)
	}

	var proInstances []DevsyProInstance
	var combinedErrs error

	for _, proInstance := range proInstanceList {
		version, err := platform.GetProInstanceDevsyVersion(
			&pkgprovider.ProInstance{Host: proInstance.Host},
		)
		combinedErrs = errors.Join(combinedErrs, err)
		proInstances = append(proInstances, DevsyProInstance{
			Host:         proInstance.Host,
			ProviderName: proInstance.Provider,
			Version:      version,
		})
	}

	return proInstances, combinedErrs
}

// PrintableError serialises a wrapped error's message via json.Marshal.
type PrintableError struct{ error }

func (p PrintableError) MarshalJSON() ([]byte, error) { return json.Marshal(p.Error()) }
