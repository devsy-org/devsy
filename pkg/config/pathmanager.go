package config

import (
	"os"
	"path/filepath"
	"sync"
)

// PathManager centralises all filesystem path computation for the Devsy CLI.
// Per-OS implementations supply the five top-level directory methods; every
// sub-path is derived from those by the shared basePathManager.
type PathManager interface {
	// Top-level XDG category directories.
	ConfigDir() (string, error)
	DataDir() (string, error)
	CacheDir() (string, error)
	StateDir() (string, error)
	RuntimeDir() (string, error)

	// Config paths.
	ConfigFilePath() (string, error)

	// Data sub-paths (context-relative).
	ContextDir(context string) (string, error)
	WorkspacesDir(context string) (string, error)
	WorkspaceDir(context, workspaceID string) (string, error)
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
}

// basePathManager implements every sub-path method by delegating the top-level
// directory resolution to the embedded pm field (the concrete per-OS struct).
type basePathManager struct {
	pm PathManager // self-reference wired after construction
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
	dir, err := b.pm.DataDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "contexts", context), nil
}

func (b *basePathManager) WorkspacesDir(context string) (string, error) {
	dir, err := b.ContextDir(context)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "workspaces"), nil
}

func (b *basePathManager) WorkspaceDir(context, workspaceID string) (string, error) {
	dir, err := b.WorkspacesDir(context)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, workspaceID), nil
}

func (b *basePathManager) MachinesDir(context string) (string, error) {
	dir, err := b.ContextDir(context)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "machines"), nil
}

func (b *basePathManager) MachineDir(context, machineID string) (string, error) {
	dir, err := b.MachinesDir(context)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, machineID), nil
}

func (b *basePathManager) ProvidersDir(context string) (string, error) {
	dir, err := b.ContextDir(context)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "providers"), nil
}

func (b *basePathManager) ProviderDir(context, providerName string) (string, error) {
	dir, err := b.ProvidersDir(context)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, providerName), nil
}

func (b *basePathManager) ProviderBinariesDir(context, providerName string) (string, error) {
	dir, err := b.ProviderDir(context, providerName)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "binaries"), nil
}

func (b *basePathManager) ProviderDaemonDir(context, providerName string) (string, error) {
	dir, err := b.ProviderDir(context, providerName)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "daemon"), nil
}

func (b *basePathManager) ProInstancesDir(context string) (string, error) {
	dir, err := b.ContextDir(context)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "pro_instances"), nil
}

func (b *basePathManager) ProInstanceDir(context, proInstanceHost string) (string, error) {
	dir, err := b.ProInstancesDir(context)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, proInstanceHost), nil
}

func (b *basePathManager) LocksDir(context string) (string, error) {
	dir, err := b.ContextDir(context)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "locks"), nil
}

// --- Cache sub-paths ---

func (b *basePathManager) AgentCacheDir() (string, error) {
	dir, err := b.pm.CacheDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "agents"), nil
}

func (b *basePathManager) ProviderDownloadCacheDir() (string, error) {
	dir, err := b.pm.CacheDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "providers"), nil
}

func (b *basePathManager) FeatureCacheDir(hashedID string) (string, error) {
	dir, err := b.pm.CacheDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "features", hashedID), nil
}

func (b *basePathManager) PlatformCacheDir() (string, error) {
	dir, err := b.pm.CacheDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "platform"), nil
}

func (b *basePathManager) SSHKeysDir() (string, error) {
	dir, err := b.pm.CacheDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "keys"), nil
}

// --- Runtime sub-paths ---

func (b *basePathManager) DaemonPIDFile() (string, error) {
	dir, err := b.pm.RuntimeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, DaemonProcessName+".pid"), nil
}

func (b *basePathManager) DaemonLockFile() (string, error) {
	dir, err := b.pm.RuntimeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, DaemonProcessName+".lock"), nil
}

func (b *basePathManager) DaemonStreamsFile() (string, error) {
	dir, err := b.pm.RuntimeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, DaemonProcessName+".streams"), nil
}

func (b *basePathManager) ProcessPIDFile(name string) (string, error) {
	dir, err := b.pm.RuntimeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, name+".pid"), nil
}

func (b *basePathManager) ProcessLockFile(name string) (string, error) {
	dir, err := b.pm.RuntimeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, name+".lock"), nil
}

func (b *basePathManager) ProcessStreamsFile(name string) (string, error) {
	dir, err := b.pm.RuntimeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, name+".streams"), nil
}

// --- State sub-paths ---

func (b *basePathManager) LogDir() (string, error) {
	dir, err := b.pm.StateDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "logs"), nil
}

// --- Singleton management ---

var (
	defaultPM     PathManager
	defaultPMOnce sync.Once
	defaultPMMu   sync.Mutex
)

// NewPathManager returns a new PathManager for the current platform.
func NewPathManager() PathManager {
	return newPlatformPathManager()
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
