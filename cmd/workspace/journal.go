package workspace

import (
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/status"
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
