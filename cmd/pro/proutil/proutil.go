// Package proutil contains shared helpers used by the 'devsy pro' command and
// its resource sub-groups (workspace, cluster, project, template). It exists to
// avoid an import cycle between cmd/pro and its child packages.
package proutil

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/pro/completion"
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// Shared table headers used across pro resource listings.
const (
	HeaderName        = "Name"
	HeaderDisplayName = "Display Name"
)

// FindProProvider resolves the devsy config and provider config for the given
// pro host.
func FindProProvider(
	ctx context.Context,
	context, provider, host string,
) (*config.Config, *providerpkg.ProviderConfig, error) {
	devsyConfig, err := config.LoadConfig(context, provider)
	if err != nil {
		return nil, nil, err
	}

	pCfg, err := workspace.ProviderFromHost(ctx, devsyConfig, host)
	if err != nil {
		return devsyConfig, nil, fmt.Errorf("load provider: %w", err)
	}

	return devsyConfig, pCfg, nil
}

// HostRunFunc runs a host-scoped pro command with its resolved devsy config and
// provider.
type HostRunFunc func(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *providerpkg.ProviderConfig,
) error

// findProProviderFn is FindProProvider behind a variable so HostRunE tests can
// stub provider resolution.
var findProProviderFn = FindProProvider

// HostRunE returns a RunE that resolves the provider for *host and passes it to
// run. *host is read when the command runs, after flag parsing.
func HostRunE(
	globalFlags *proflags.GlobalFlags,
	host *string,
	run HostRunFunc,
) func(*cobra.Command, []string) error {
	return func(cobraCmd *cobra.Command, _ []string) error {
		ctx := cobraCmd.Context()
		devsyConfig, pCfg, err := findProProviderFn(
			ctx,
			globalFlags.Context,
			globalFlags.Provider,
			*host,
		)
		if err != nil {
			return err
		}
		return run(ctx, devsyConfig, pCfg)
	}
}

type hostFlagOptions struct {
	completionFlags *proflags.GlobalFlags
}

// HostFlagOption configures AddHostFlag.
type HostFlagOption func(*hostFlagOptions)

// WithHostCompletion registers shell completion for --host listing pro instances.
func WithHostCompletion(globalFlags *proflags.GlobalFlags) HostFlagOption {
	return func(o *hostFlagOptions) { o.completionFlags = globalFlags }
}

// AddHostFlag registers the required --host flag, binds its env var, and
// applies opts.
func AddHostFlag(c *cobra.Command, dest *string, opts ...HostFlagOption) {
	var o hostFlagOptions
	for _, opt := range opts {
		opt(&o)
	}

	cliflags.Add(c, cliflags.String(dest, names.Host, "", "The pro instance to use"))
	_ = c.MarkFlagRequired(names.Host)
	cliflags.BindEnv(c.Flags(), names.Host)

	gf := o.completionFlags
	if gf == nil {
		return
	}
	_ = c.RegisterFlagCompletionFunc(names.Host, func(
		rootCmd *cobra.Command,
		args []string,
		toComplete string,
	) ([]string, cobra.ShellCompDirective) {
		return completion.GetPlatformHostSuggestions(
			rootCmd, gf.Context, gf.Provider, args, toComplete, gf.Owner,
		)
	})
}

// MarkAgentExecuted flags the root command as executed by an agent. Use it as a
// PersistentPreRun.
func MarkAgentExecuted(cmd *cobra.Command, _ []string) {
	root := cmd.Root()
	if root.Annotations == nil {
		root.Annotations = map[string]string{}
	}
	root.Annotations[config.AgentExecutedAnnotation] = "true"
}
