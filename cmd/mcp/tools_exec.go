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
	IDLabels       []string          `json:"id_labels,omitempty"`
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
	// Error carries the classified error payload when the exec failed with
	// partial output already captured. The MCP SDK overwrites a result's
	// StructuredContent with the marshalled typed output, so the classification
	// has to ride inside execOutput itself to survive the round-trip.
	Error *ErrorPayload `json:"error,omitempty"`
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
			IDLabels:              in.IDLabels,
			TimeoutSeconds:        in.TimeoutSeconds,
			TimeoutSecondsDefault: durationToSeconds(cmd.ExecTimeoutDefault),
			TimeoutSecondsMax:     durationToSeconds(cmd.ExecTimeoutMax),
			Owner:                 cmd.Owner,
			Context:               cmd.Context,
			Provider:              cmd.Provider,
			Stdout:                stdout,
			Stderr:                stderr,
		})
		// Populate output from whatever was captured. A cancelled or timed-out
		// exec may still have written partial stdout/stderr that's useful to
		// the caller, so read the buffers unconditionally.
		out := execOutput{
			Stdout:    stdout.String(),
			Stderr:    stderr.String(),
			Truncated: stdout.Truncated() || stderr.Truncated(),
		}
		if res != nil {
			out.ExitCode = res.ExitCode
			out.DurationMS = res.DurationMS
			out.TimedOut = res.TimedOut
			out.Clamped = res.Clamped
		}
		if err != nil {
			payload := ClassifyError(err)
			out.Error = &payload
			return errorResult(err), out, nil
		}
		return nil, out, nil
	}))
}
