package up

import (
	"context"
	"sync"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/workspacejournal"
)

func withJournal(reporter status.Reporter, workspaceID string) status.Reporter {
	journal, err := workspacejournal.OpenDefault()
	if err != nil {
		log.Debugf("workspace operation journal unavailable: %v", err)
		return reporter
	}
	r := &journalStatusReporter{
		presenter: reporter,
		journal:   status.ForPipeline(journal.Reporter(workspaceID), status.PipelineWorkspaceUp),
		gate:      terminalStatusGate(reporter),
	}
	if r.gate != nil {
		r.gate.mu.Lock()
		r.gate.journal = r
		r.gate.mu.Unlock()
	}
	return r
}

func terminalStatusGate(reporter status.Reporter) *interactiveStatusGate {
	switch r := reporter.(type) {
	case *interactiveStatusGate:
		return r
	case *journalStatusReporter:
		return r.gate
	default:
		return nil
	}
}

type journalStatusReporter struct {
	presenter status.Reporter
	journal   status.Reporter
	gate      *interactiveStatusGate
	mu        sync.Mutex
	events    []status.Event
	active    bool
	running   bool
	idle      chan struct{}
}

func (r *journalStatusReporter) Report(event status.Event) {
	r.presenter.Report(event)
	for {
		r.mu.Lock()
		if r.active {
			r.events = append(r.events, event)
			if !r.running {
				r.running = true
				r.idle = make(chan struct{})
				go r.run()
			}
			r.mu.Unlock()
			return
		}
		if r.running {
			idle := r.idle
			r.mu.Unlock()
			<-idle
			continue
		}
		r.running = true
		r.idle = make(chan struct{})
		r.mu.Unlock()
		r.journal.Report(event)
		r.mu.Lock()
		if len(r.events) > 0 {
			go r.run()
		} else {
			r.running = false
			close(r.idle)
		}
		r.mu.Unlock()
		return
	}
}

func (r *journalStatusReporter) begin() {
	r.mu.Lock()
	r.active = true
	r.mu.Unlock()
}

func (r *journalStatusReporter) end(ctx context.Context) error {
	r.mu.Lock()
	r.active = false
	idle := r.idle
	running := r.running
	r.mu.Unlock()
	if !running {
		return nil
	}
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *journalStatusReporter) run() {
	for {
		r.mu.Lock()
		if len(r.events) == 0 {
			r.running = false
			close(r.idle)
			r.mu.Unlock()
			return
		}
		event := r.events[0]
		r.events[0] = status.Event{}
		r.events = r.events[1:]
		r.mu.Unlock()
		r.journal.Report(event)
	}
}
