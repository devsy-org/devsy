package framework

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/devsy-org/devsy/pkg/workspace"
)

// DevsyWorkspaceEditJSON executes `devsy workspace edit` with JSON output and
// returns the raw stdout. A failing edit returns an error carrying stderr.
func (f *Framework) DevsyWorkspaceEditJSON(
	ctx context.Context,
	id string,
	args ...string,
) (string, error) {
	baseArgs := []string{cmdWorkspace, "edit", id, flagResultFormat, formatJSON}
	baseArgs = append(baseArgs, args...)
	stdout, stderr, err := f.ExecCommandCapture(ctx, baseArgs)
	if err != nil {
		return "", fmt.Errorf("devsy workspace edit failed: %w (stderr: %s)", err, stderr)
	}

	return stdout, nil
}

// DevsyWorkspaceEditResult executes `devsy workspace edit` and decodes the
// JSON presentation result.
func (f *Framework) DevsyWorkspaceEditResult(
	ctx context.Context,
	id string,
	args ...string,
) (*workspace.PresentationResult, error) {
	stdout, err := f.DevsyWorkspaceEditJSON(ctx, id, args...)
	if err != nil {
		return nil, err
	}

	result := &workspace.PresentationResult{}
	if err := json.Unmarshal([]byte(stdout), result); err != nil {
		return nil, fmt.Errorf("decode workspace edit result %q: %w", stdout, err)
	}

	return result, nil
}
