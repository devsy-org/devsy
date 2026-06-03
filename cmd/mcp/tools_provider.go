package mcp

import (
	"context"
	"fmt"
	"sort"

	"github.com/devsy-org/devsy/cmd/flags"
	cmdprovider "github.com/devsy-org/devsy/cmd/provider"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/workspace"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type providerSummary struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Default bool   `json:"default,omitempty"`
}

type providerListOutput struct {
	Providers []providerSummary `json:"providers"`
}

type providerAddInput struct {
	Source  string            `json:"source"            jsonschema:"required"`
	Name    string            `json:"name,omitempty"`
	Options map[string]string `json:"options,omitempty"`
	Use     bool              `json:"use,omitempty"`
}

type providerNameInput struct {
	Name string `json:"name" jsonschema:"required"`
}

func registerProviderTools(s *sdkmcp.Server, g *flags.GlobalFlags) {
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "provider_list",
		Description: "List configured Devsy providers.",
	}, safeHandler(func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ struct{},
	) (*sdkmcp.CallToolResult, providerListOutput, error) {
		out, err := handleProviderList(ctx, g)
		if err != nil {
			return errorResult(err), providerListOutput{}, nil
		}
		return nil, out, nil
	}))

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "provider_add",
		Description: "Add a provider from a source (registry name, URL, or local path).",
	}, safeHandler(func(
		ctx context.Context, _ *sdkmcp.CallToolRequest, in providerAddInput,
	) (*sdkmcp.CallToolResult, opOK, error) {
		if err := runProviderAdd(ctx, g, in); err != nil {
			return errorResult(err), opOK{}, nil
		}
		return nil, opOK{OK: true}, nil
	}))

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "provider_delete",
		Description: "Delete a configured provider.",
	}, safeHandler(func(
		ctx context.Context, _ *sdkmcp.CallToolRequest, in providerNameInput,
	) (*sdkmcp.CallToolResult, opOK, error) {
		if err := runProviderDelete(ctx, g, in.Name); err != nil {
			return errorResult(err), opOK{}, nil
		}
		return nil, opOK{OK: true}, nil
	}))

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "provider_use",
		Description: "Set a provider as the default for new workspaces.",
	}, safeHandler(func(
		ctx context.Context, _ *sdkmcp.CallToolRequest, in providerNameInput,
	) (*sdkmcp.CallToolResult, opOK, error) {
		if err := runProviderUse(ctx, g, in.Name); err != nil {
			return errorResult(err), opOK{}, nil
		}
		return nil, opOK{OK: true}, nil
	}))
}

func handleProviderList(_ context.Context, g *flags.GlobalFlags) (providerListOutput, error) {
	devsyConfig, err := config.LoadConfig(g.Context, g.Provider)
	if err != nil {
		return providerListOutput{}, err
	}

	providers, err := workspace.LoadAllProviders(devsyConfig)
	if err != nil {
		return providerListOutput{}, err
	}

	defaultProvider := devsyConfig.Current().DefaultProvider

	summaries := make([]providerSummary, 0, len(providers))
	for _, entry := range providers {
		summaries = append(summaries, providerSummary{
			Name:    entry.Config.Name,
			Version: entry.Config.Version,
			Default: entry.Config.Name == defaultProvider,
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Name < summaries[j].Name
	})

	return providerListOutput{Providers: summaries}, nil
}

func runProviderAdd(ctx context.Context, g *flags.GlobalFlags, in providerAddInput) error {
	args := []string{}
	if in.Name != "" {
		args = append(args, "--name", in.Name)
	}
	if in.Use {
		args = append(args, "--use")
	}
	for k, v := range in.Options {
		args = append(args, "--option", fmt.Sprintf("%s=%s", k, v))
	}
	args = append(args, "--")
	args = append(args, in.Source)

	cobraCmd := cmdprovider.NewAddCmd(g)
	cobraCmd.SetArgs(args)
	cobraCmd.SetContext(ctx)
	return cobraCmd.Execute()
}

func runProviderDelete(ctx context.Context, g *flags.GlobalFlags, name string) error {
	cobraCmd := cmdprovider.NewDeleteCmd(g)
	cobraCmd.SetArgs([]string{"--", name})
	cobraCmd.SetContext(ctx)
	return cobraCmd.Execute()
}

func runProviderUse(ctx context.Context, g *flags.GlobalFlags, name string) error {
	cobraCmd := cmdprovider.NewUseCmd(g)
	cobraCmd.SetArgs([]string{"--", name})
	cobraCmd.SetContext(ctx)
	return cobraCmd.Execute()
}
