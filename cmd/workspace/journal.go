package workspace

import (
	"context"

	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/devsy-org/devsy/pkg/workspacejournal"
)

func withWorkspaceJournal(reporter status.Reporter, workspaceID string) status.Reporter {
	journal, err := workspacejournal.OpenDefault()
	if err != nil {
		log.Debugf("workspace operation journal unavailable: %v", err)
		return reporter
	}
	return status.Tee(
		reporter,
		status.ForPipeline(journal.Reporter(workspaceID), status.PipelineWorkspaceUp),
	)
}

// journalWorkspaceKey returns the map key resolveDeleteTargets uses for a
// delete target list.
func journalWorkspaceKey(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// resolvedDeleteTarget pairs a delete argument with the workspace client
// resolved from it. Client is nil when the argument could not be resolved;
// the deletion path then retries with the raw argument and the journal falls
// back to the argument as its workspace key.
type resolvedDeleteTarget struct {
	ID     string
	Client client2.BaseWorkspaceClient
}

// resolveDeleteTargets resolves each delete target exactly once so the same
// resolved client drives both deletion and journal keying. It falls back to
// the raw argument when a target cannot be resolved, such as a broken
// workspace removed with --force.
func resolveDeleteTargets(
	ctx context.Context,
	devsyConfig *config.Config,
	owner platform.OwnerFilter,
	args []string,
) map[string]resolvedDeleteTarget {
	targets := args
	if len(targets) == 0 {
		targets = []string{""}
	}
	resolved := make(map[string]resolvedDeleteTarget, len(targets))
	for _, arg := range targets {
		resolved[arg] = resolvedDeleteTarget{ID: arg}
		callArgs := args
		if len(args) > 1 {
			callArgs = []string{arg}
		}
		client, err := workspace.Get(ctx, workspace.GetOptions{
			DevsyConfig: devsyConfig,
			Args:        callArgs,
			Owner:       owner,
		})
		if err != nil {
			log.Debugf("workspace operation journal: resolve workspace %q: %v", arg, err)
			continue
		}
		resolved[arg] = resolvedDeleteTarget{ID: client.Workspace(), Client: client}
	}
	return resolved
}
