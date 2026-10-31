package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
)

// DevsySetupLogs runs `workspace logs --source setup` and returns stdout and stderr.
func (f *Framework) DevsySetupLogs(ctx context.Context, args ...string) (string, string, error) {
	full := append([]string{cmdWorkspace, "logs", "--source", "setup"}, args...)
	stdout, stderr, err := f.ExecCommandCapture(ctx, full)
	if err != nil {
		return stdout, stderr, fmt.Errorf("devsy setup logs failed: %w (stderr: %s)", err, stderr)
	}

	return stdout, stderr, nil
}

// DevsySetupLogsRead reads every page of a run as JSON and merges the records.
func (f *Framework) DevsySetupLogsRead(
	ctx context.Context,
	args ...string,
) (*workspacelogs.ReadResponse, error) {
	var merged *workspacelogs.ReadResponse
	after := ""
	for {
		pageArgs := append([]string{flagResultFormat, formatJSON}, args...)
		if after != "" {
			pageArgs = append(pageArgs, "--after", after)
		}
		stdout, _, err := f.DevsySetupLogs(ctx, pageArgs...)
		if err != nil {
			return nil, err
		}
		page := &workspacelogs.ReadResponse{}
		if err := json.Unmarshal([]byte(stdout), page); err != nil {
			return nil, fmt.Errorf("setup logs page is not valid JSON: %w", err)
		}
		if merged == nil {
			merged = page
		} else {
			merged.Records = append(merged.Records, page.Records...)
			merged.Complete, merged.HasMore = page.Complete, page.HasMore
			merged.Availability = page.Availability
		}
		if !page.HasMore {
			return merged, nil
		}
		after = page.NextCursor
	}
}

// DevsySetupRuns lists retained runs, newest first. Pass a workspace to scope it.
func (f *Framework) DevsySetupRuns(
	ctx context.Context,
	args ...string,
) (*workspacelogs.IndexResponse, error) {
	listArgs := append([]string{"--list", flagResultFormat, formatJSON, "--limit", "100"}, args...)
	stdout, _, err := f.DevsySetupLogs(ctx, listArgs...)
	if err != nil {
		return nil, err
	}
	resp := &workspacelogs.IndexResponse{}
	if err := json.Unmarshal([]byte(stdout), resp); err != nil {
		return nil, fmt.Errorf("setup run index is not valid JSON: %w", err)
	}

	return resp, nil
}

// SetupLogsText joins the output text of one stream. An empty stream joins all.
func SetupLogsText(resp *workspacelogs.ReadResponse, stream workspacelogs.Stream) string {
	var sb strings.Builder
	for i := range resp.Records {
		rec := &resp.Records[i]
		if rec.Kind == workspacelogs.KindOutput && (stream == "" || rec.Stream == stream) {
			sb.WriteString(rec.Text)
		}
	}

	return sb.String()
}

// SetupInvocations returns the last transition of each invocation, keyed by ID.
func SetupInvocations(resp *workspacelogs.ReadResponse) map[string]workspacelogs.Record {
	final := map[string]workspacelogs.Record{}
	for i := range resp.Records {
		if rec := resp.Records[i]; rec.Kind == workspacelogs.KindInvocation {
			final[rec.InvocationID] = rec
		}
	}

	return final
}

// SetupInvocationsFor returns the final transitions of one hook.
func SetupInvocationsFor(
	resp *workspacelogs.ReadResponse,
	hook string,
) []workspacelogs.Record {
	var out []workspacelogs.Record
	for _, rec := range SetupInvocations(resp) {
		if rec.Hook == hook {
			out = append(out, rec)
		}
	}

	return out
}

// SetupLogsDir returns the local directory that holds retained setup history.
func SetupLogsDir() (string, error) {
	return config.DefaultPathManager().WorkspaceLogsDir()
}

// FindInFiles returns the files under dir whose bytes contain needle.
func FindInFiles(dir, needle string) ([]string, error) {
	var hits []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		// #nosec G304 G122 -- walks a test-owned directory.
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), needle) {
			hits = append(hits, path)
		}

		return nil
	})

	return hits, err
}
