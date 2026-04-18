package completion

import (
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/devsy-org/log"
	"github.com/spf13/cobra"
)

func GetPlatformHostSuggestions(
	rootCmd *cobra.Command,
	context, provider string,
	args []string,
	toComplete string,
	owner platform.OwnerFilter,
	logger log.Logger,
) ([]string, cobra.ShellCompDirective) {
	devsyConfig, err := config.LoadConfig(context, provider)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	proInstances, err := workspace.ListProInstances(devsyConfig, logger)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var suggestions []string

	for _, instance := range proInstances {
		if strings.HasPrefix(instance.Host, toComplete) {
			suggestions = append(suggestions, instance.Host)
		}
	}

	return suggestions, cobra.ShellCompDirectiveNoFileComp
}
