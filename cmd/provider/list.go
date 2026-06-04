package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	devsyhttp "github.com/devsy-org/devsy/pkg/http"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// ListCmd holds the list cmd flags.
type ListCmd struct {
	*flags.GlobalFlags
	Available bool
}

// NewListCmd creates a new command.
func NewListCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &ListCmd{
		GlobalFlags: flags,
	}
	listCmd := &cobra.Command{
		Use:         "list",
		Aliases:     []string{"ls"},
		Short:       "List providers",
		Args:        cobra.NoArgs,
		Annotations: telemetry.SkipInUIAnnotation(),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	listCmd.Flags().
		BoolVar(&cmd.Available, "available", false, "List providers available for installation rather than installed ones")

	return listCmd
}

type ProviderWithDefault struct {
	workspace.ProviderWithOptions `json:",inline"`

	Default bool `json:"default,omitempty"`
}

// Run runs the command logic.
func (cmd *ListCmd) Run(ctx context.Context) error {
	if cmd.Available {
		return cmd.runAvailable(ctx)
	}
	return cmd.runInstalled(ctx)
}

// runInstalled lists installed providers.
func (cmd *ListCmd) runInstalled(_ context.Context) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	providers, err := workspace.LoadAllProviders(devsyConfig)
	if err != nil {
		return err
	}

	configuredProviders := devsyConfig.Current().Providers
	if configuredProviders == nil {
		configuredProviders = map[string]*config.ProviderConfig{}
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	switch mode {
	case output.ModePlain:
		return cmd.renderInstalledPlain(devsyConfig, providers)
	case output.ModeJSON:
		return cmd.renderInstalledJSON(devsyConfig, configuredProviders, providers)
	}

	return nil
}

// renderInstalledPlain renders installed providers in plain text format.
func (cmd *ListCmd) renderInstalledPlain(
	devsyConfig *config.Config,
	providers map[string]*workspace.ProviderWithOptions,
) error {
	tableEntries := [][]string{}
	for _, entry := range providers {
		tableEntries = append(tableEntries, []string{
			entry.Config.Name,
			entry.Config.Version,
			strconv.FormatBool(devsyConfig.Current().DefaultProvider == entry.Config.Name),
			strconv.FormatBool(entry.State != nil && entry.State.Initialized),
			entry.Config.Description,
		})
	}
	sort.SliceStable(tableEntries, func(i, j int) bool {
		return tableEntries[i][0] < tableEntries[j][0]
	})

	table.Print([]string{
		"Name",
		"Version",
		"Default",
		"Initialized",
		"Description",
	}, tableEntries)

	return nil
}

// renderInstalledJSON renders installed providers in JSON format.
func (cmd *ListCmd) renderInstalledJSON(
	devsyConfig *config.Config,
	configuredProviders map[string]*config.ProviderConfig,
	providers map[string]*workspace.ProviderWithOptions,
) error {
	retMap := map[string]ProviderWithDefault{}
	for k, entry := range providers {
		var dynamicOptions map[string]*types.Option
		if configuredProviders[entry.Config.Name] != nil {
			dynamicOptions = configuredProviders[entry.Config.Name].DynamicOptions
		}

		srcOptions := MergeDynamicOptions(entry.Config.Options, dynamicOptions)
		entry.Config.Options = srcOptions
		retMap[k] = ProviderWithDefault{
			ProviderWithOptions: *entry,
			Default:             devsyConfig.Current().DefaultProvider == entry.Config.Name,
		}
	}

	out, err := json.MarshalIndent(retMap, "", "  ")
	if err != nil {
		return err
	}
	//nolint:forbidigo
	fmt.Print(string(out))

	return nil
}

// runAvailable lists providers available for installation.
func (cmd *ListCmd) runAvailable(ctx context.Context) error {
	jsonResult, err := fetchProviderRepos(ctx)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintln(os.Stdout, "List of available providers from "+config.RepoOwner+":")
	var rows [][]string
	for _, v := range jsonResult {
		name, ok := v["name"].(string)
		if !ok || name == "" {
			continue
		}
		if after, ok0 := strings.CutPrefix(name, config.ProviderPrefix); ok0 {
			rows = append(rows, []string{after})
		}
	}
	table.Print([]string{"Provider"}, rows)

	return nil
}

func fetchProviderRepos(ctx context.Context) ([]map[string]any, error) {
	const perPage = 100
	var all []map[string]any
	for page := 1; ; page++ {
		pageRepos, err := fetchProviderReposPage(ctx, page, perPage)
		if err != nil {
			return nil, err
		}
		all = append(all, pageRepos...)
		if len(pageRepos) < perPage {
			return all, nil
		}
	}
}

func fetchProviderReposPage(ctx context.Context, page, perPage int) ([]map[string]any, error) {
	url := fmt.Sprintf("%s/repos?per_page=%d&page=%d", config.GitHubAPIUserURL, perPage, page)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := devsyhttp.GetHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var pageRepos []map[string]any
	if err := json.Unmarshal(body, &pageRepos); err != nil {
		return nil, err
	}
	return pageRepos, nil
}
