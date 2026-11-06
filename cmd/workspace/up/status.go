package up

import (
	"fmt"
	"io"
	"sync"

	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/status"
)

// newStatusReporter drives `up`'s progress output.
func newStatusReporter(
	resultFormat string,
	out io.Writer,
	verbose bool,
	noStatus bool,
) (status.Reporter, error) {
	reporter, err := status.NewCLIReporter(status.CLIOptions{
		Format:   resultFormat,
		Out:      out,
		Prefix:   "up",
		Verbose:  verbose,
		NoStatus: noStatus,
		Pipeline: status.PipelineWorkspaceUp,
		Labels:   phaseLabels,
		Envelope: func(e status.Event) error {
			return config2.WriteStatusJSON(out, e)
		},
	})
	if err != nil {
		return nil, err
	}
	return newInteractiveStatusGate(reporter, out, noStatus), nil
}

var phaseLabels = map[status.Phase]string{
	status.PhaseCloningRepository:     "cloning repository",
	status.PhaseResolvingConfig:       "resolving devcontainer config",
	status.PhaseInitializeCommand:     "running initializeCommand",
	status.PhasePreparingDevContainer: "preparing devcontainer",
	status.PhaseBuildingImage:         "building image",
	status.PhaseStartingContainer:     "starting container",
	status.PhaseInjectingAgent:        "injecting agent",
	status.PhaseRunningLifecycleHook:  "running lifecycle hooks",
	status.PhaseWaitingFor:            "waiting for readiness",
	status.PhaseConfiguringWorkspace:  "configuring workspace",
	status.PhaseConfiguringSSH:        "configuring SSH",
	status.PhaseStartingSSHTunnel:     "starting SSH tunnel",
	status.PhaseLaunchingIDE:          "launching IDE",
	status.PhaseReady:                 "ready",
}

type interactiveStatusGate struct {
	mu       sync.Mutex
	renderMu sync.Mutex
	active   bool
	enabled  bool
	events   []status.Event
	omitted  int
	reporter status.Reporter
	out      io.Writer
	quiet    bool
	running  bool
	journal  *journalStatusReporter
}

func newInteractiveStatusGate(
	reporter status.Reporter,
	out io.Writer,
	quiet bool,
) *interactiveStatusGate {
	return &interactiveStatusGate{reporter: reporter, out: out, quiet: quiet}
}

func (g *interactiveStatusGate) Report(event status.Event) {
	g.mu.Lock()
	if !g.enabled {
		g.mu.Unlock()
		g.renderMu.Lock()
		g.mu.Lock()
		if !g.enabled {
			g.mu.Unlock()
			g.reporter.Report(event)
			g.renderMu.Unlock()
			return
		}
		g.renderMu.Unlock()
	}
	if len(g.events) < 256 && g.omitted == 0 {
		g.events = append(g.events, event)
	} else {
		g.omitted++
	}
	g.startLocked()
	g.mu.Unlock()
}

func (g *interactiveStatusGate) SetActive(active bool) {
	g.mu.Lock()
	g.active = active
	g.startLocked()
	g.mu.Unlock()
	if active {
		g.waitForRender()
	}
}

func (g *interactiveStatusGate) startLocked() {
	if !g.active && !g.running && (len(g.events) > 0 || g.omitted > 0) {
		g.running = true
		go g.run()
	}
}

func (g *interactiveStatusGate) waitForRender() {
	g.renderMu.Lock()
	defer g.renderMu.Unlock()
}

func (g *interactiveStatusGate) run() {
	for {
		g.renderMu.Lock()
		g.mu.Lock()
		if g.active || (len(g.events) == 0 && g.omitted == 0) {
			g.running = false
			g.mu.Unlock()
			g.renderMu.Unlock()
			return
		}
		if len(g.events) > 0 {
			event := g.events[0]
			g.events = g.events[1:]
			g.mu.Unlock()
			g.reporter.Report(event)
		} else {
			count := g.omitted
			g.omitted = 0
			g.mu.Unlock()
			if !g.quiet {
				_, _ = fmt.Fprintf(g.out, "%d progress updates omitted\n", count)
			}
		}
		g.renderMu.Unlock()
	}
}
