package workspace

import (
	"context"

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

// journalWorkspaceKey returns the map key resolveJournalWorkspaceIDs uses for
// a delete target list.
func journalWorkspaceKey(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// resolveJournalWorkspaceIDs maps each delete target to its resolved workspace
// ID so journaled events are stored under the same ID readers query with. It
// falls back to the raw argument when a target cannot be resolved.
func resolveJournalWorkspaceIDs(
	ctx context.Context,
	devsyConfig *config.Config,
	owner platform.OwnerFilter,
	args []string,
) map[string]string {
	targets := args
	if len(targets) == 0 {
		targets = []string{""}
	}
	ids := make(map[string]string, len(targets))
	for _, arg := range targets {
		ids[arg] = arg
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
		ids[arg] = client.Workspace()
	}
	return ids
}
