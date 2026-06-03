package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/workspace"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type workspaceSummary struct {
	Name     string `json:"name"`
	Provider string `json:"provider,omitempty"`
	IDE      string `json:"ide,omitempty"`
	Source   string `json:"source,omitempty"`
	LastUsed string `json:"last_used,omitempty"`
}

type (
	workspaceListInput  struct{}
	workspaceListOutput struct {
		Workspaces []workspaceSummary `json:"workspaces"`
	}
)

type workspaceStatusInput struct {
	Name string `json:"name" jsonschema:"required"`
}

func registerWorkspaceTools(s *sdkmcp.Server, g *flags.GlobalFlags) {
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "workspace_list",
		Description: "List all Devsy workspaces.",
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ workspaceListInput,
	) (*sdkmcp.CallToolResult, workspaceListOutput, error) {
		out, err := handleWorkspaceList(ctx, g)
		if err != nil {
			return errorResult(err), workspaceListOutput{}, nil
		}
		return nil, out, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "workspace_status",
		Description: "Get detailed status for a workspace by name.",
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in workspaceStatusInput) (*sdkmcp.CallToolResult, any, error) {
		out, err := handleWorkspaceStatus(ctx, g, in.Name)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return nil, out, nil
	})

	registerWorkspaceLifecycleTools(s, g)
}

func handleWorkspaceList(ctx context.Context, g *flags.GlobalFlags) (workspaceListOutput, error) {
	devsyConfig, err := config.LoadConfig(g.Context, g.Provider)
	if err != nil {
		return workspaceListOutput{}, err
	}
	entries, err := workspace.List(ctx, devsyConfig, false, g.Owner)
	if err != nil {
		return workspaceListOutput{}, err
	}
	summaries := make([]workspaceSummary, 0, len(entries))
	for _, e := range entries {
		summaries = append(summaries, workspaceSummary{
			Name:     e.ID,
			Provider: e.Provider.Name,
			IDE:      e.IDE.Name,
			Source:   e.Source.String(),
			LastUsed: e.LastUsedTimestamp.Format(time.RFC3339),
		})
	}
	return workspaceListOutput{Workspaces: summaries}, nil
}

func handleWorkspaceStatus(ctx context.Context, g *flags.GlobalFlags, name string) (any, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	devsyConfig, err := config.LoadConfig(g.Context, g.Provider)
	if err != nil {
		return nil, err
	}
	client, err := workspace.Get(ctx, workspace.GetOptions{
		DevsyConfig: devsyConfig,
		Args:        []string{name},
		Owner:       g.Owner,
	})
	if err != nil {
		return nil, err
	}
	return client.WorkspaceConfig(), nil
}

// errorResult builds an isError CallToolResult carrying our structured payload.
func errorResult(err error) *sdkmcp.CallToolResult {
	payload := ClassifyError(err)
	return &sdkmcp.CallToolResult{
		IsError:           true,
		Content:           []sdkmcp.Content{&sdkmcp.TextContent{Text: payload.Message}},
		StructuredContent: payload,
	}
}

// Stub — Task 7 replaces this.
func registerWorkspaceLifecycleTools(_ *sdkmcp.Server, _ *flags.GlobalFlags) {}
