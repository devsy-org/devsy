package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// joinUnder returns elems joined beneath the directory produced by parent.
// A parent error is returned unchanged.
func joinUnder(parent func() (string, error), elems ...string) (string, error) {
	dir, err := parent()
	if err != nil {
		return "", err
	}

	return filepath.Join(append([]string{dir}, elems...)...), nil
}

// PathManager centralises filesystem paths. Layout:
//
//	ConfigDir()/DataDir()  (same directory today; both may be $DEVSY_HOME)
//	├── config.yaml                        (ConfigFilePath, stamped with Config.SchemaVersion)
//	└── contexts/<context>/                (ContextDir)
//	    ├── workspaces/<id>/{agent,logs}    (WorkspaceDir, WorkspaceAgentDir, WorkspaceLogDir)
//	    ├── contents/<id>                   (WorkspaceContentsDir/WorkspaceContentDir - parent never removed)
//	    ├── machines/<id>                   (MachineDir)
//	    ├── providers/<name>/{binaries,daemon} (ProviderDir)
//	    ├── pro_instances/<id>              (ProInstanceDir)
//	    └── locks/                          (LocksDir)
//	CacheDir()                              (default $HOME/.cache/devsy, or $DEVSY_HOME/cache when set)
//	├── agents/, providers/, features/<hash>/, platform/, keys/
//	StateDir()                              (default under DataDir()/state)
//	RuntimeDir()                            (default under DataDir()/run on darwin,
//	                                         OS temp dir on linux; see pathmanager_darwin.go / pathmanager_linux.go)
type PathManager interface {
	// Top-level XDG category directories.
	ConfigDir() (string, error)
	DataDir() (string, error)
	CacheDir() (string, error)
	StateDir() (string, error)
	RuntimeDir() (string, error)

	// SystemBinDir is a directory on the user's PATH where standalone tool
	// binaries (e.g. a git-lfs release fallback) can be installed.
	SystemBinDir() (string, error)

	// Dependency roots are shared across contexts. Resolving them may create
	// the parent category roots, but does not create dependency subdirectories.
	DependencyToolsDir() (string, error)
	DependencyDownloadCacheDir() (string, error)
	DependencyStateDir() (string, error)
	DependencyLocksDir() (string, error)

	// Config paths.
	ConfigFilePath() (string, error)

	// Data sub-paths (context-relative).
	ContextDir(context string) (string, error)
	WorkspacesDir(context string) (string, error)
	WorkspaceDir(context, workspaceID string) (string, error)
	WorkspaceAgentDir(context, workspaceID string) (string, error)
	WorkspaceContentsDir(context string) (string, error)
	WorkspaceContentDir(context, workspaceID string) (string, error)
	WorkspaceLogDir(context, workspaceID string) (string, error)
	MachinesDir(context string) (string, error)
	MachineDir(context, machineID string) (string, error)
	ProvidersDir(context string) (string, error)
	ProviderDir(context, providerName string) (string, error)
	ProviderBinariesDir(context, providerName string) (string, error)
	ProviderDaemonDir(context, providerName string) (string, error)
	ProInstancesDir(context string) (string, error)
	ProInstanceDir(context, proInstanceHost string) (string, error)
	LocksDir(context string) (string, error)

	// Cache sub-paths.
	AgentCacheDir() (string, error)
	ProviderDownloadCacheDir() (string, error)
	FeatureCacheDir(hashedID string) (string, error)
	PlatformCacheDir() (string, error)
	SSHKeysDir() (string, error)

	// Runtime sub-paths.
	DaemonPIDFile() (string, error)
	DaemonLockFile() (string, error)
	DaemonStreamsFile() (string, error)
	ProcessPIDFile(name string) (string, error)
	ProcessLockFile(name string) (string, error)
	ProcessStreamsFile(name string) (string, error)

	// State sub-paths.
	LogDir() (string, error)
	TaskDir() (string, error)
}

// basePathManager implements every sub-path method by delegating the top-level
// directory resolution to the embedded pm field (the concrete per-OS struct).
type basePathManager struct {
	pm       PathManager // self-reference wired after construction
	readOnly bool
}

func (b *basePathManager) DependencyToolsDir() (string, error) {
	return joinUnder(b.pm.DataDir, "tools")
}

func (b *basePathManager) DependencyDownloadCacheDir() (string, error) {
	return joinUnder(b.pm.CacheDir, "dependencies")
}

func (b *basePathManager) DependencyStateDir() (string, error) {
	return joinUnder(b.pm.StateDir, "dependencies")
}

func (b *basePathManager) DependencyLocksDir() (string, error) {
	return joinUnder(b.DependencyStateDir, "locks")
}

// --- Config paths ---

func (b *basePathManager) ConfigFilePath() (string, error) {
	if p := os.Getenv(EnvConfig); p != "" {
		return p, nil
	}

	dir, err := b.pm.ConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, ConfigFile), nil
}

// --- Data sub-paths (context-relative) ---

func (b *basePathManager) ContextDir(context string) (string, error) {
	return joinUnder(b.pm.DataDir, "contexts", context)
}

func (b *basePathManager) WorkspacesDir(context string) (string, error) {
	return b.inContext(context, "workspaces")
}

func (b *basePathManager) WorkspaceDir(context, workspaceID string) (string, error) {
	return b.inContext(context, "workspaces", workspaceID)
}

// WorkspaceAgentDir is where host-side `devsy internal agent workspace ...` invocations
// store their per-workspace state. It lives under WorkspaceDir so a single
// os.RemoveAll(WorkspaceDir) wipes it during workspace delete. Container-side
// agent invocations still resolve their home via FindAgentHomeDir and are
// unaffected.
func (b *basePathManager) WorkspaceAgentDir(context, workspaceID string) (string, error) {
	return b.inContext(context, "workspaces", workspaceID, "agent")
}

// WorkspaceContentsDir is the per-context parent of workspace content folders.
// It is never removed on delete, keeping a stable host inode for the bind-mount
// source's parent across up -> delete -> up (avoids Docker Desktop's stale
// file-share inode cache).
func (b *basePathManager) WorkspaceContentsDir(context string) (string, error) {
	return b.inContext(context, "contents")
}

// WorkspaceContentDir is the bind-mount source leaf under WorkspaceContentsDir.
func (b *basePathManager) WorkspaceContentDir(context, workspaceID string) (string, error) {
	return b.inContext(context, "contents", workspaceID)
}

// WorkspaceLogDir is the per-workspace log directory used by the desktop's
// streaming-log store. Lives under WorkspaceDir for the same reason as
// WorkspaceAgentDir.
func (b *basePathManager) WorkspaceLogDir(context, workspaceID string) (string, error) {
	return b.inContext(context, "workspaces", workspaceID, "logs")
}

func (b *basePathManager) MachinesDir(context string) (string, error) {
	return b.inContext(context, "machines")
}

func (b *basePathManager) MachineDir(context, machineID string) (string, error) {
	return b.inContext(context, "machines", machineID)
}

func (b *basePathManager) ProvidersDir(context string) (string, error) {
	return b.inContext(context, "providers")
}

func (b *basePathManager) ProviderDir(context, providerName string) (string, error) {
	return b.inContext(context, "providers", providerName)
}

func (b *basePathManager) ProviderBinariesDir(context, providerName string) (string, error) {
	return b.inContext(context, "providers", providerName, "binaries")
}

func (b *basePathManager) ProviderDaemonDir(context, providerName string) (string, error) {
	return b.inContext(context, "providers", providerName, "daemon")
}

func (b *basePathManager) ProInstancesDir(context string) (string, error) {
	return b.inContext(context, "pro_instances")
}

func (b *basePathManager) ProInstanceDir(context, proInstanceHost string) (string, error) {
	return b.inContext(context, "pro_instances", proInstanceHost)
}

func (b *basePathManager) LocksDir(context string) (string, error) {
	return b.inContext(context, "locks")
}

// --- Cache sub-paths ---

func (b *basePathManager) AgentCacheDir() (string, error) {
	return joinUnder(b.pm.CacheDir, "agents")
}

func (b *basePathManager) ProviderDownloadCacheDir() (string, error) {
	return joinUnder(b.pm.CacheDir, "providers")
}

func (b *basePathManager) FeatureCacheDir(hashedID string) (string, error) {
	return joinUnder(b.pm.CacheDir, "features", hashedID)
}

func (b *basePathManager) PlatformCacheDir() (string, error) {
	return joinUnder(b.pm.CacheDir, "platform")
}

func (b *basePathManager) SSHKeysDir() (string, error) {
	return joinUnder(b.pm.CacheDir, "keys")
}

// --- Runtime sub-paths ---

func (b *basePathManager) DaemonPIDFile() (string, error) {
	return joinUnder(b.pm.RuntimeDir, DaemonProcessName+".pid")
}

func (b *basePathManager) DaemonLockFile() (string, error) {
	return joinUnder(b.pm.RuntimeDir, DaemonProcessName+".lock")
}

func (b *basePathManager) DaemonStreamsFile() (string, error) {
	return joinUnder(b.pm.RuntimeDir, DaemonProcessName+".streams")
}

func (b *basePathManager) ProcessPIDFile(name string) (string, error) {
	return joinUnder(b.pm.RuntimeDir, name+".pid")
}

func (b *basePathManager) ProcessLockFile(name string) (string, error) {
	return joinUnder(b.pm.RuntimeDir, name+".lock")
}

func (b *basePathManager) ProcessStreamsFile(name string) (string, error) {
	return joinUnder(b.pm.RuntimeDir, name+".streams")
}

// --- State sub-paths ---

func (b *basePathManager) LogDir() (string, error) {
	return joinUnder(b.pm.StateDir, "logs")
}

// TaskDir holds state files for detached background tasks.
func (b *basePathManager) TaskDir() (string, error) {
	return joinUnder(b.pm.StateDir, "tasks")
}

func (b *basePathManager) ensureDir(path string) (string, error) {
	if b.readOnly {
		return path, nil
	}

	// #nosec G703 -- paths are derived from user-controlled env vars (XDG_*) or
	// well-known OS defaults; path traversal is not a concern for a CLI tool
	// running as the invoking user.
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", fmt.Errorf("create directory %s: %w", path, err)
	}

	return path, nil
}

// homeOverrideDir roots sub beneath DEVSY_HOME when set, returning ok=false so
// callers fall through to the OS-native default when it is unset.
func (b *basePathManager) homeOverrideDir(sub ...string) (string, bool, error) {
	home := os.Getenv(EnvHome)
	if home == "" {
		return "", false, nil
	}

	dir, err := b.ensureDir(filepath.Join(append([]string{home}, sub...)...))

	return dir, true, err
}

// inContext returns elems joined beneath ContextDir(context).
func (b *basePathManager) inContext(context string, elems ...string) (string, error) {
	return joinUnder(func() (string, error) { return b.ContextDir(context) }, elems...)
}

var (
	defaultPM     PathManager
	defaultPMOnce sync.Once
	defaultPMMu   sync.Mutex
)

// NewPathManager returns a new PathManager for the current platform.
func NewPathManager() PathManager {
	return newPlatformPathManager(false)
}

// NewReadOnlyPathManager resolves the same paths as NewPathManager without
// creating directories. Returned paths need not exist or be writable.
func NewReadOnlyPathManager() PathManager {
	return newPlatformPathManager(true)
}

// DefaultPathManager returns the process-wide singleton PathManager.
func DefaultPathManager() PathManager {
	defaultPMMu.Lock()
	defer defaultPMMu.Unlock()

	defaultPMOnce.Do(func() {
		defaultPM = NewPathManager()
	})

	return defaultPM
}

// SetPathManager replaces the singleton PathManager (for testing).
func SetPathManager(pm PathManager) {
	defaultPMMu.Lock()
	defer defaultPMMu.Unlock()

	defaultPM = pm
	// Mark the Once as done so future DefaultPathManager() calls return pm.
	defaultPMOnce.Do(func() {})
}

// ResetPathManager clears the singleton so the next DefaultPathManager call
// creates a fresh instance. Intended for tests only.
func ResetPathManager() {
	defaultPMMu.Lock()
	defer defaultPMMu.Unlock()

	defaultPM = nil
	defaultPMOnce = sync.Once{}
}
