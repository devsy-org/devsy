//go:build !windows

package agentcontainer

import (
	"encoding/json"
	"os"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/setup"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
)

// setupRunRef is the non-secret reference a background worker uses to join the
// container's run. The producer ID was reserved by the launching process.
type setupRunRef struct {
	RunID            string `json:"runId"`
	ProducerID       string `json:"producerId"`
	ParentProducerID string `json:"parentProducerId"`
}

// containerRun is the container setup process's view of the run: its own
// session and the producers it reserved for the workers it may launch.
type containerRun struct {
	session      *workspacelogs.Session
	deferredID   string
	postAttachID string
}

// startContainerRun joins the run named by the CLI options in the container's
// spool. It returns nil when no run is requested or history cannot be kept.
func startContainerRun(
	info *provider2.ContainerWorkspaceInfo,
	secretEntries []string,
) *containerRun {
	ref := info.SetupRun
	if ref == nil || !workspacelogs.ValidID(ref.RunID) {
		return nil
	}
	root := setup.WorkspaceLogsRoot()
	if root == "" {
		return nil
	}
	store, err := workspacelogs.Open(workspacelogs.Options{Root: root})
	if err != nil {
		log.Debugf("setup history unavailable: %v", err)
		return nil
	}
	session := workspacelogs.StartSessionIn(store, workspacelogs.SessionOptions{
		RunID:            ref.RunID,
		ParentProducerID: validParent(ref.ParentProducerID),
		Location:         workspacelogs.LocationContainer,
		Redactor:         runRedactor(secretEntries),
		Run: workspacelogs.RunSpec{
			Context:            ref.Context,
			WorkspaceUID:       ref.WorkspaceUID,
			WorkspaceIDAtStart: ref.WorkspaceID,
			Command:            "up",
		},
	})
	if session == nil {
		return nil
	}

	return &containerRun{session: session}
}

func validParent(id string) string {
	if workspacelogs.ValidID(id) {
		return id
	}

	return ""
}

func runRedactor(secretEntries []string) *secrets.Redactor {
	return secrets.Combine(
		secrets.NewEnvironmentRedactor(os.Environ()),
		secrets.NewRedactor(secretEntries),
	)
}

// producerID returns this process's producer ID, or empty without history.
func (r *containerRun) producerID() string {
	if r == nil {
		return ""
	}

	return r.session.ProducerID()
}

// reserveWorkers reserves a producer for each worker that will be launched, so
// the run knows about it before it starts.
func (r *containerRun) reserveWorkers(deferred, postAttach bool) {
	if r == nil {
		return
	}
	if deferred {
		r.deferredID = r.session.Recorder.ReserveChild(workspacelogs.LocationContainer)
	}
	if postAttach {
		r.postAttachID = r.session.Recorder.ReserveChild(workspacelogs.LocationContainer)
	}
}

func (r *containerRun) deferredChild() string {
	if r == nil {
		return ""
	}

	return r.deferredID
}

func (r *containerRun) postAttachChild() string {
	if r == nil {
		return ""
	}

	return r.postAttachID
}

// sealDiscovery closes producer discovery once every worker is reserved or
// excluded.
func (r *containerRun) sealDiscovery() {
	if r != nil {
		r.session.Recorder.SealDiscovery()
	}
}

// launchOutcome records what is known about a reserved worker after its launch
// call. A nil error does not prove this run's worker started, so a launch that
// built no command is recorded as unavailable instead of an empty success.
func (r *containerRun) launchOutcome(childID string, built bool, err error) {
	if r == nil || childID == "" {
		return
	}
	switch {
	case err != nil:
		r.session.Recorder.MarkChild(childID, workspacelogs.ProducerUnavailable, "launch_failed")
	case !built:
		r.session.Recorder.MarkChild(childID, workspacelogs.ProducerUnavailable, "already_running")
	}
}

// workerEnv appends the run reference for a worker to env (the current
// environment when env is nil).
func (r *containerRun) workerEnv(env []string, childID string) []string {
	if r == nil || childID == "" {
		return env
	}
	ref, err := json.Marshal(setupRunRef{
		RunID: r.session.RunID, ProducerID: childID, ParentProducerID: r.producerID(),
	})
	if err != nil {
		return env
	}
	if env == nil {
		env = os.Environ()
	}

	return append(env, pkgconfig.EnvSetupRun+"="+string(ref))
}

// finish records the container process's outcome.
func (r *containerRun) finish(err error) {
	if r != nil {
		r.session.Finish(err, false)
	}
}

// startWorkerRun joins the run a background worker was launched for. It reads
// and clears the reference so hooks do not inherit it. It returns nil when the
// worker was launched without history.
func startWorkerRun(secretEntries []string) *workspacelogs.Session {
	raw := os.Getenv(pkgconfig.EnvSetupRun)
	_ = os.Unsetenv(pkgconfig.EnvSetupRun)
	if raw == "" {
		return nil
	}
	var ref setupRunRef
	if json.Unmarshal([]byte(raw), &ref) != nil || !workspacelogs.ValidID(ref.RunID) ||
		!workspacelogs.ValidID(ref.ProducerID) {
		return nil
	}
	root := setup.WorkspaceLogsRoot()
	if root == "" {
		return nil
	}
	store, err := workspacelogs.Open(workspacelogs.Options{Root: root})
	if err != nil {
		return nil
	}
	m, err := store.Manifest(ref.RunID)
	if err != nil {
		return nil
	}

	return workspacelogs.StartSessionIn(store, workspacelogs.SessionOptions{
		RunID:            ref.RunID,
		ProducerID:       ref.ProducerID,
		ParentProducerID: validParent(ref.ParentProducerID),
		Location:         workspacelogs.LocationContainer,
		Redactor:         runRedactor(secretEntries),
		Run: workspacelogs.RunSpec{
			Context: m.Context, Command: m.Command, WorkspaceUID: workspaceUIDOf(m),
		},
	})
}

func workspaceUIDOf(m *workspacelogs.RunManifest) string {
	if m.WorkspaceUID == nil {
		return ""
	}

	return *m.WorkspaceUID
}
