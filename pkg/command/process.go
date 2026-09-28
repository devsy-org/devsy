package command

// ProcessTreeKind identifies the platform primitive that owns a worker tree.
type ProcessTreeKind string

const (
	ProcessTreeLegacyPID  ProcessTreeKind = "legacy-pid"
	ProcessTreeUnixGroup  ProcessTreeKind = "unix-group-v1"
	ProcessTreeWindowsJob ProcessTreeKind = "windows-job-v1"
)

// ProcessRef is the persisted reference used to supervise a detached worker.
// TreeID and Identity are opaque outside this package.
type ProcessRef struct {
	PID      int             `json:"pid,omitempty"`
	TreeKind ProcessTreeKind `json:"treeKind,omitempty"`
	TreeID   string          `json:"treeId,omitempty"`
	Identity string          `json:"identity,omitempty"`
}

// ProcessController provides the two operations needed by task cancellation.
type ProcessController interface {
	Terminate(ProcessRef) error
	CleanupAfterExit(ProcessRef) error
}

type processController struct{}

func DefaultProcessController() ProcessController { return processController{} }

func (processController) Terminate(ref ProcessRef) error {
	return terminateProcessRef(ref)
}

func (processController) CleanupAfterExit(ref ProcessRef) error {
	return cleanupExitedProcessRef(ref)
}

func IsRunning(pid string) (bool, error) {
	return isRunning(pid)
}

func Kill(pid string) error {
	return killTree(pid, "")
}

// KillTree terminates the process and everything it spawned: the job or
// process group treeName identifies when set, otherwise the process alone.
func KillTree(pid, treeName string) error {
	return killTree(pid, treeName)
}

// KillTreeWithIdentity terminates a worker tree using its saved platform identity.
func KillTreeWithIdentity(pid, treeName, identity string) error {
	return killTreeWithIdentity(pid, treeName, identity)
}

// KillTreeAfterWorkerExit terminates descendants using the worker's saved session identity.
func KillTreeAfterWorkerExit(pid, treeName, identity string) error {
	return killTreeAfterWorkerExit(pid, treeName, identity)
}

// ProcessTreeIdentity returns the kernel identity used to distinguish a reused process ID.
func ProcessTreeIdentity(pid int) (string, error) {
	return processTreeIdentity(pid)
}
