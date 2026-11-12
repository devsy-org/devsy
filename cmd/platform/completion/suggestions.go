package completion

import (
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// SuggestionRequest carries the inputs of the completion helpers.
type SuggestionRequest struct {
	Context    string
	Provider   string
	Args       []string
	ToComplete string
	Owner      platform.OwnerFilter
}

func GetPlatformHostSuggestions(
	rootCmd *cobra.Command,
	req SuggestionRequest,
) ([]string, cobra.ShellCompDirective) {
	context, provider, toComplete := req.Context, req.Provider, req.ToComplete
	devsyConfig, err := config.LoadConfig(context, provider)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	platformInstances, err := workspace.ListPlatformInstances(devsyConfig)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var suggestions []string

	for _, instance := range platformInstances {
		if strings.HasPrefix(instance.Host, toComplete) {
			suggestions = append(suggestions, instance.Host)
		}
	}

	return suggestions, cobra.ShellCompDirectiveNoFileComp
}
