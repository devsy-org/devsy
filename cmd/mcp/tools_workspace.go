package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	up "github.com/devsy-org/devsy/cmd/workspace/up"
	client2 "github.com/devsy-org/devsy/pkg/client"
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

type nameInput struct {
	Name  string `json:"name"            jsonschema:"required"`
	Force bool   `json:"force,omitempty"`
}

type opOK struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type createInput struct {
	Source           string `json:"source"                      jsonschema:"required"`
	Name             string `json:"name,omitempty"`
	Provider         string `json:"provider,omitempty"`
	IDE              string `json:"ide,omitempty"`
	DevcontainerPath string `json:"devcontainer_path,omitempty"`
}

func registerWorkspaceLifecycleTools(s *sdkmcp.Server, g *flags.GlobalFlags) {
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "workspace_start",
		Description: "Start (or resume) an existing workspace by name.",
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in nameInput) (*sdkmcp.CallToolResult, opOK, error) {
		if err := startWorkspace(ctx, g, in.Name); err != nil {
			return errorResult(err), opOK{}, nil
		}
		return nil, opOK{OK: true}, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "workspace_stop",
		Description: "Stop a running workspace by name.",
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in nameInput) (*sdkmcp.CallToolResult, opOK, error) {
		if err := stopWorkspace(ctx, g, in.Name); err != nil {
			return errorResult(err), opOK{}, nil
		}
		return nil, opOK{OK: true}, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "workspace_delete",
		Description: "Delete a workspace by name. Pass force=true to force-delete even if not found remotely.",
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in nameInput) (*sdkmcp.CallToolResult, opOK, error) {
		if err := deleteWorkspace(ctx, g, in.Name, in.Force); err != nil {
			return errorResult(err), opOK{}, nil
		}
		return nil, opOK{OK: true}, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "workspace_create",
		Description: "Create and start a new workspace from a git URL, local path, or container image.",
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in createInput) (*sdkmcp.CallToolResult, any, error) {
		out, err := createWorkspace(ctx, g, in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return nil, out, nil
	})
}

func startWorkspace(ctx context.Context, g *flags.GlobalFlags, name string) error {
	return runUp(ctx, g, createInput{Source: name})
}

func stopWorkspace(ctx context.Context, g *flags.GlobalFlags, name string) error {
	devsyConfig, err := config.LoadConfig(g.Context, g.Provider)
	if err != nil {
		return err
	}
	client, err := workspace.Get(ctx, workspace.GetOptions{
		DevsyConfig: devsyConfig,
		Args:        []string{name},
		Owner:       g.Owner,
	})
	if err != nil {
		return err
	}
	return client.Stop(ctx, client2.StopOptions{})
}

func deleteWorkspace(ctx context.Context, g *flags.GlobalFlags, name string, force bool) error {
	devsyConfig, err := config.LoadConfig(g.Context, g.Provider)
	if err != nil {
		return err
	}
	_, err = workspace.Delete(ctx, workspace.DeleteOptions{
		DevsyConfig: devsyConfig,
		Args:        []string{name},
		Force:       force,
		Owner:       g.Owner,
	})
	return err
}

func createWorkspace(ctx context.Context, g *flags.GlobalFlags, in createInput) (any, error) {
	if in.Source == "" {
		return nil, fmt.Errorf("source is required")
	}
	if err := runUp(ctx, g, in); err != nil {
		return nil, err
	}
	devsyConfig, err := config.LoadConfig(g.Context, g.Provider)
	if err != nil {
		return nil, err
	}
	lookup := in.Name
	if lookup == "" {
		lookup = in.Source
	}
	client, err := workspace.Get(ctx, workspace.GetOptions{
		DevsyConfig: devsyConfig,
		Args:        []string{lookup},
		Owner:       g.Owner,
	})
	if err != nil {
		return nil, err
	}
	return client.WorkspaceConfig(), nil
}

func runUp(ctx context.Context, g *flags.GlobalFlags, in createInput) error {
	args := []string{}
	if in.Provider != "" {
		args = append(args, "--provider", in.Provider)
	}
	if in.IDE != "" {
		args = append(args, "--ide", in.IDE)
	} else {
		args = append(args, "--ide=none")
	}
	if in.DevcontainerPath != "" {
		args = append(args, "--devcontainer-path", in.DevcontainerPath)
	}
	if in.Name != "" {
		args = append(args, "--id", in.Name)
	}
	args = append(args, in.Source)

	cobraCmd := up.NewUpCmd(g)
	cobraCmd.SetArgs(args)
	cobraCmd.SetContext(ctx)
	return cobraCmd.Execute()
}
