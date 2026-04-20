package completion

import (
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/workspace"
	oldlog "github.com/devsy-org/log"
	"github.com/spf13/cobra"
)

func GetPlatformHostSuggestions(
	rootCmd *cobra.Command,
	context, provider string,
	args []string,
	toComplete string,
	owner platform.OwnerFilter,
) ([]string, cobra.ShellCompDirective) {
	devsyConfig, err := config.LoadConfig(context, provider)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	proInstances, err := workspace.ListProInstances(devsyConfig, oldlog.Default)
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
