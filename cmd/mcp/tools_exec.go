package mcp

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/devsy-org/devsy/pkg/workspace"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// durationToSeconds converts a Duration to a positive whole number of seconds,
// rounding up so that any non-zero sub-second value resolves to at least 1s
// rather than truncating to 0 (which would silently fall through to defaults).
func durationToSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(math.Ceil(d.Seconds()))
}

type execInput struct {
	Name           string            `json:"name"                      jsonschema:"required"`
	Command        []string          `json:"command"                   jsonschema:"required"`
	Workdir        string            `json:"workdir,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

type execOutput struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Truncated  bool   `json:"truncated"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	Clamped    bool   `json:"clamped,omitempty"`
}

func registerExecTool(s *sdkmcp.Server, cmd *ServeCmd) {
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "workspace_exec",
		Description: "Run a one-shot command in a workspace container. Output is capped " +
			"per stream; excess is truncated in the middle. The command is argv, not a shell string.",
	}, safeHandler(func(
		ctx context.Context, _ *sdkmcp.CallToolRequest, in execInput,
	) (*sdkmcp.CallToolResult, execOutput, error) {
		if in.Name == "" {
			return errorResult(fmt.Errorf("name is required")), execOutput{}, nil
		}
		if len(in.Command) == 0 {
			return errorResult(fmt.Errorf("command is required")), execOutput{}, nil
		}
		stdout := NewBoundedBuffer(cmd.ExecOutputCap)
		stderr := NewBoundedBuffer(cmd.ExecOutputCap)

		res, err := workspace.ExecOneShot(ctx, workspace.ExecOneShotOptions{
			WorkspaceName:         in.Name,
			Command:               in.Command,
			Workdir:               in.Workdir,
			Env:                   in.Env,
			TimeoutSeconds:        in.TimeoutSeconds,
			TimeoutSecondsDefault: durationToSeconds(cmd.ExecTimeoutDefault),
			TimeoutSecondsMax:     durationToSeconds(cmd.ExecTimeoutMax),
			Owner:                 cmd.Owner,
			Context:               cmd.Context,
			Provider:              cmd.Provider,
			Stdout:                stdout,
			Stderr:                stderr,
		})
		if err != nil {
			return errorResult(err), execOutput{}, nil
		}
		return nil, execOutput{
			Stdout:     stdout.String(),
			Stderr:     stderr.String(),
			ExitCode:   res.ExitCode,
			DurationMS: res.DurationMS,
			Truncated:  stdout.Truncated() || stderr.Truncated(),
			TimedOut:   res.TimedOut,
			Clamped:    res.Clamped,
		}, nil
	}))
}
