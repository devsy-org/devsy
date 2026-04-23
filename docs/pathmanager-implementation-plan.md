# PathManager Implementation Plan

Centralize all file storage under a single `PathManager` interface with XDG Base Directory compliance on Linux, platform-native conventions on macOS/Windows, and full backward compatibility with `DEVSY_HOME`.

## Problem

The codebase has **6 independent storage systems** with paths computed ad-hoc in different packages:

| # | System | Location | Package |
|---|--------|----------|---------|
| 1 | Central root | `$DEVSY_HOME` or `~/.devsy` | `pkg/config/dir.go` |
| 2 | Context-relative data | `<configDir>/contexts/<ctx>/{workspaces,machines,providers,pro,locks}` | `pkg/provider/dir.go` |
| 3 | Legacy `.loft` cache | `$DEVSY_CACHE_FOLDER` or `~/.loft/config.json` | `pkg/platform/client/client.go` |
| 4 | Agent binary cache | `os.TempDir()/devsy-cache` | `pkg/agent/binary.go` |
| 5 | Provider binary cache | `os.TempDir()` fallback `os.UserCacheDir()` / `devsy-binaries` | `pkg/provider/download.go` |
| 6 | Feature extraction cache | `os.TempDir()/devsy/features/<hash>` | `pkg/devcontainer/feature/features.go` |

Additional scattered `os.TempDir()` usage:
- Daemon PID/lock/streams files (`pkg/command/background.go`, `pkg/daemon/agent/daemon.go`)
- SSH key storage (`pkg/ssh/keys.go`) — falls back from `~/.devsy/keys` to `os.TempDir()`
- SSH agent sockets (`pkg/ssh/server/agent.go`)
- Docker credential helper log (`pkg/dockercredentials/helper.go`)
- Pprof debug port file (`cmd/profile.go`) — hardcoded `/tmp/pprof_ports`
- Helm chart temp workdirs (`cmd/pro/start.go`)

None of these follow XDG conventions, and the daemon PID/lock files in `os.TempDir()` are lost on reboot (which is correct for runtime data, but the current placement is accidental, not intentional).

## Target XDG Layout

```
XDG_CONFIG_HOME/devsy/          (~/.config/devsy)
├── config.yaml                  # main config file
└── ssh/                         # SSH config fragments (future)

XDG_DATA_HOME/devsy/             (~/.local/share/devsy)
└── contexts/
    └── <context>/
        ├── workspaces/          # workspace state
        ├── machines/            # machine state
        ├── providers/           # provider configs and binaries
        ├── pro/                 # pro instance configs
        └── locks/               # context-level locks

XDG_CACHE_HOME/devsy/            (~/.cache/devsy)
├── agents/                      # agent binary cache (was: /tmp/devsy-cache)
├── providers/                   # provider binary download cache (was: /tmp or XDG_CACHE)
├── features/                    # devcontainer feature cache (was: /tmp/devsy/features)
├── platform/                    # platform auth tokens (was: ~/.loft/config.json)
└── keys/                        # SSH host/public keys (was: ~/.devsy/keys)

XDG_STATE_HOME/devsy/            (~/.local/state/devsy)
├── logs/                        # credential helper logs, daemon stream logs
└── pprof_ports                  # debug profiling info

XDG_RUNTIME_DIR/devsy/           (/run/user/UID/devsy)
├── devsy.daemon.pid             # daemon PID file
├── devsy.daemon.lock            # daemon lock file
├── devsy.daemon.streams         # daemon stdout/stderr
├── ssh-agent-<id>/              # SSH agent sockets
└── <process>.{pid,lock,streams} # other background processes (IDE daemons, etc.)
```

### macOS Mapping

| XDG Category | macOS Path |
|---|---|
| ConfigDir | `~/Library/Application Support/devsy` |
| DataDir | `~/Library/Application Support/devsy` |
| CacheDir | `~/Library/Caches/devsy` |
| StateDir | `~/Library/Application Support/devsy/state` |
| RuntimeDir | `os.TempDir()/devsy-<uid>` (no native equivalent) |

### Windows Mapping

| XDG Category | Windows Path |
|---|---|
| ConfigDir | `%APPDATA%\devsy` |
| DataDir | `%LOCALAPPDATA%\devsy` |
| CacheDir | `%LOCALAPPDATA%\devsy\cache` |
| StateDir | `%LOCALAPPDATA%\devsy\state` |
| RuntimeDir | `%TEMP%\devsy` |

### Legacy Mode (`DEVSY_HOME` set)

When `$DEVSY_HOME` is set, **all** categories collapse into that single directory to preserve backward compatibility:

```
$DEVSY_HOME/
├── config.yaml
├── contexts/
├── cache/
│   ├── agents/
│   ├── providers/
│   ├── features/
│   └── platform/
├── state/
│   └── logs/
├── keys/
└── run/
    ├── devsy.daemon.pid
    └── devsy.daemon.lock
```

---

## PathManager Interface Design

```go
// pkg/config/pathmanager.go

package config

// PathManager provides all filesystem paths used by the application.
// Implementations handle per-OS conventions (XDG on Linux, Library on macOS,
// AppData on Windows) and the legacy DEVSY_HOME single-root mode.
type PathManager interface {
    // Top-level XDG category directories
    ConfigDir() (string, error)  // config files (config.yaml)
    DataDir() (string, error)    // persistent data (contexts, workspaces, machines)
    CacheDir() (string, error)   // regenerable caches (binaries, features)
    StateDir() (string, error)   // non-essential state (logs)
    RuntimeDir() (string, error) // ephemeral runtime (PIDs, locks, sockets)

    // Config paths
    ConfigFilePath() (string, error) // full path to config.yaml

    // Data sub-paths (context-relative)
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

    // Cache sub-paths
    AgentCacheDir() (string, error)           // agent binary cache
    ProviderDownloadCacheDir() (string, error) // provider binary download cache
    FeatureCacheDir(hashedID string) (string, error) // devcontainer feature cache
    PlatformCacheDir() (string, error)        // platform auth tokens (was ~/.loft)
    SSHKeysDir() (string, error)              // SSH host/public keys

    // Runtime sub-paths
    DaemonPIDFile() (string, error)
    DaemonLockFile() (string, error)
    DaemonStreamsFile() (string, error)
    ProcessPIDFile(name string) (string, error)
    ProcessLockFile(name string) (string, error)
    ProcessStreamsFile(name string) (string, error)

    // State sub-paths
    LogDir() (string, error)
}
```

### Construction

```go
// NewPathManager returns a PathManager appropriate for the current platform.
// If DEVSY_HOME is set, returns a legacy single-root implementation.
// If DEVSY_CONFIG is set, it overrides only the config file path.
func NewPathManager() PathManager {
    if home := os.Getenv(EnvHome); home != "" {
        return &legacyPathManager{root: home}
    }
    return newPlatformPathManager()
}

// newPlatformPathManager is defined per-OS via build tags:
//   pathmanager_linux.go  → XDG
//   pathmanager_darwin.go → ~/Library
//   pathmanager_windows.go → %APPDATA% / %LOCALAPPDATA%
```

### Singleton vs Dependency Injection

The current codebase uses package-level functions (`config.GetConfigDir()`, `provider.GetWorkspacesDir(ctx)`). A global singleton is the pragmatic first step:

```go
var defaultPathManager PathManager
var initOnce sync.Once

func DefaultPathManager() PathManager {
    initOnce.Do(func() {
        defaultPathManager = NewPathManager()
    })
    return defaultPathManager
}

// SetPathManager allows tests to inject a custom implementation.
func SetPathManager(pm PathManager) {
    defaultPathManager = pm
}
```

---

## Implementation Phases

### Phase 1: PathManager Interface + Per-OS Defaults + Tests

**Goal**: Introduce the `PathManager` type and per-OS implementations. No callsite changes — existing code continues to work unchanged. This phase is a pure addition.

**Files to create**:
| File | Purpose |
|---|---|
| `pkg/config/pathmanager.go` | Interface, `NewPathManager()`, `DefaultPathManager()`, `SetPathManager()` |
| `pkg/config/pathmanager_linux.go` | XDG implementation (`//go:build linux`) |
| `pkg/config/pathmanager_darwin.go` | macOS `~/Library` implementation (`//go:build darwin`) |
| `pkg/config/pathmanager_windows.go` | Windows `%APPDATA%`/`%LOCALAPPDATA%` implementation (`//go:build windows`) |
| `pkg/config/pathmanager_legacy.go` | `legacyPathManager` — everything under a single root when `DEVSY_HOME` is set |
| `pkg/config/pathmanager_test.go` | Unit tests for all implementations |

**Acceptance criteria**:
- [ ] `PathManager` interface is defined and exported
- [ ] Linux implementation reads `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_CACHE_HOME`, `XDG_STATE_HOME`, `XDG_RUNTIME_DIR` with correct fallback defaults
- [ ] macOS implementation uses `~/Library/Application Support/devsy` and `~/Library/Caches/devsy`
- [ ] Windows implementation uses `%APPDATA%\devsy`, `%LOCALAPPDATA%\devsy`
- [ ] Legacy implementation puts everything under `$DEVSY_HOME` with subdirectories
- [ ] `DEVSY_CONFIG` override is respected for `ConfigFilePath()`
- [ ] All context-relative sub-path methods produce correct paths
- [ ] `DefaultPathManager()` returns a singleton; `SetPathManager()` allows test injection
- [ ] Unit tests cover: each OS implementation (via direct struct construction), legacy mode, env var overrides, `DEVSY_CONFIG` override, context sub-paths
- [ ] All existing tests still pass — no behavioral change

**Testing approach**:
- Test each implementation struct directly (not through build tags) by constructing them explicitly
- Use `t.Setenv()` to test env var overrides
- Test that legacy mode with `DEVSY_HOME=/custom` produces all paths under `/custom`

---

### Phase 2: Migrate `pkg/config/dir.go` and `pkg/provider/dir.go`

**Goal**: Replace the two central path-computing modules with thin wrappers around `PathManager`. All 10+ functions in `provider/dir.go` that call `config.GetConfigDir()` now delegate to `DefaultPathManager()`.

**Files to modify**:
| File | Change |
|---|---|
| `pkg/config/dir.go` | `GetConfigDir()` delegates to `DefaultPathManager().DataDir()` (or `.ConfigDir()` when only config.yaml lookup); `GetConfigPath()` delegates to `DefaultPathManager().ConfigFilePath()` |
| `pkg/provider/dir.go` | All 10 `Get*Dir()` functions delegate to `DefaultPathManager()` methods |

**Files to modify (callers — no functional change, just verify)**:
- All callers of `config.GetConfigDir()` and `provider.Get*Dir()` — these keep calling the same functions, but the implementation behind them changes.

**Acceptance criteria**:
- [ ] `config.GetConfigDir()` and `config.GetConfigPath()` still exist with the same signatures (no breaking API change)
- [ ] All `provider.Get*Dir()` functions still exist with the same signatures
- [ ] When `DEVSY_HOME` is set, behavior is identical to the current implementation
- [ ] When `DEVSY_HOME` is not set, Linux users get XDG paths instead of `~/.devsy` (only for **new** installations — Phase 5 handles migration)
- [ ] All existing tests pass

**Key design decision**: `GetConfigDir()` currently returns the root of everything (`~/.devsy`). After this phase:
- It returns `DataDir()` — since contexts/workspaces/machines are data, not config
- A new `GetConfigDir()` semantic: callers that only need `config.yaml` get `ConfigDir()`
- This is the trickiest semantic split. We may need to audit each `GetConfigDir()` caller to decide if it wants config or data. The safe approach: `GetConfigDir()` continues to return `DataDir()` in Phase 2 to avoid behavioral changes, and we split in a later phase.

**Safe approach for Phase 2**:
```go
// GetConfigDir returns the data directory for backward compat.
// Callers that only need config.yaml should use GetConfigPath().
func GetConfigDir() (string, error) {
    return DefaultPathManager().DataDir()
}
```

---

### Phase 3: Migrate Legacy `.loft` Cache

**Goal**: Move platform auth token storage from `~/.loft/config.json` to `PathManager.PlatformCacheDir()`.

**Files to modify**:
| File | Change |
|---|---|
| `pkg/platform/client/client.go` | Replace `init()` that builds `CacheFolder` and `DefaultCacheConfig` with `PathManager` calls |
| `pkg/config/env.go` | Add `EnvCacheFolder = "DEVSY_CACHE_FOLDER"` constant (currently a raw string in client.go) |

**Backward compatibility**:
- If `DEVSY_CACHE_FOLDER` is set, use it (existing behavior)
- If `~/.loft/config.json` exists but the new path doesn't, read from old location (migration in Phase 5)
- New installations write to `PathManager.PlatformCacheDir()`

**Acceptance criteria**:
- [ ] `client.go` no longer computes its own paths in `init()`
- [ ] `DEVSY_CACHE_FOLDER` override still works
- [ ] New installations store platform config at `<CacheDir>/platform/config.json`
- [ ] Existing `~/.loft/config.json` is still found and loaded (read fallback)
- [ ] Tests verify both new-path and legacy-path loading

---

### Phase 4: Migrate Temp Dir Usages

**Goal**: Move all `os.TempDir()` usages to appropriate `PathManager` directories.

#### 4a: Agent Binary Cache (`pkg/agent/binary.go`)

| File | Change |
|---|---|
| `pkg/agent/binary.go:29` | `os.TempDir()` → `DefaultPathManager().AgentCacheDir()` |

**Current**: `os.TempDir()/devsy-cache`
**New**: `<CacheDir>/agents/`

#### 4b: Provider Binary Download Cache (`pkg/provider/download.go`)

| File | Change |
|---|---|
| `pkg/provider/download.go:289-295` | `getCachedBinaryPath()` → `DefaultPathManager().ProviderDownloadCacheDir()` |

**Current**: `os.TempDir()` with `os.UserCacheDir()` upgrade, under `devsy-binaries/`
**New**: `<CacheDir>/providers/`

#### 4c: Devcontainer Feature Cache (`pkg/devcontainer/feature/features.go`)

| File | Change |
|---|---|
| `pkg/devcontainer/feature/features.go:379-381` | `getFeaturesTempFolder()` → `DefaultPathManager().FeatureCacheDir(hashedID)` |

**Current**: `os.TempDir()/devsy/features/<hashedID>`
**New**: `<CacheDir>/features/<hashedID>`

#### 4d: Daemon PID/Lock/Streams Files (`pkg/command/background.go`)

| File | Change |
|---|---|
| `pkg/command/background.go:24-26` | `os.TempDir()` → `DefaultPathManager().Process{PID,Lock,Streams}File(commandName)` |
| `pkg/daemon/agent/daemon.go:309` | `os.TempDir()` → `DefaultPathManager().DaemonPIDFile()` |

**Current**: `os.TempDir()/devsy.daemon.{pid,lock,streams}`
**New**: `<RuntimeDir>/devsy.daemon.{pid,lock,streams}`

**Important**: `background.go` is generic — it takes a `commandName` and creates `{commandName}.{pid,lock,streams}`. Callers include IDE daemons (rstudio, openvscode, jupyter, fleet). We need `ProcessPIDFile(name)`, `ProcessLockFile(name)`, `ProcessStreamsFile(name)` that all use `RuntimeDir`.

#### 4e: SSH Keys (`pkg/ssh/keys.go`)

| File | Change |
|---|---|
| `pkg/ssh/keys.go:85-98` | `GetDevsyKeysDir()` → `DefaultPathManager().SSHKeysDir()` |

**Current**: `~/.devsy/keys` with `os.TempDir()/devsy-ssh` fallback
**New**: `<CacheDir>/keys/`

#### 4f: SSH Agent Socket (`pkg/ssh/server/agent.go`)

| File | Change |
|---|---|
| `pkg/ssh/server/agent.go:24` | `os.TempDir()` → `DefaultPathManager().RuntimeDir()` + sub-path |

**Current**: `os.TempDir()/auth-agent-<id>`
**New**: `<RuntimeDir>/auth-agent-<id>`

#### 4g: Docker Credential Helper Log (`pkg/dockercredentials/helper.go`)

| File | Change |
|---|---|
| `pkg/dockercredentials/helper.go:166` | `os.TempDir()` → `DefaultPathManager().LogDir()` |

**Current**: `os.TempDir()/devsy-docker-credential-helper.log`
**New**: `<StateDir>/logs/devsy-docker-credential-helper.log`

#### 4h: Pprof Debug Ports (`cmd/profile.go`)

| File | Change |
|---|---|
| `cmd/profile.go:26` | `/tmp/pprof_ports` → `DefaultPathManager().StateDir()` + `pprof_ports` |

**Current**: Hardcoded `/tmp/pprof_ports`
**New**: `<StateDir>/pprof_ports`

#### 4i: Helm Chart Temp Dirs (`cmd/pro/start.go`)

| File | Change |
|---|---|
| `cmd/pro/start.go:1957` | Leave as `os.TempDir()` — these are genuinely temporary working dirs for helm operations, not cache |

**No change** — `os.TempDir()` is correct here. Helm workdirs are truly ephemeral.

**Acceptance criteria for Phase 4**:
- [ ] No remaining `os.TempDir()` calls for devsy-specific storage (only legitimate temp file usage like helm workdirs and `os.CreateTemp`)
- [ ] Agent binaries cached in `<CacheDir>/agents/`
- [ ] Provider binaries cached in `<CacheDir>/providers/`
- [ ] Features cached in `<CacheDir>/features/`
- [ ] Platform tokens in `<CacheDir>/platform/`
- [ ] SSH keys in `<CacheDir>/keys/`
- [ ] Daemon PIDs/locks in `<RuntimeDir>/`
- [ ] Logs in `<StateDir>/logs/`
- [ ] All IDE background processes use `RuntimeDir` for PID/lock files
- [ ] `cmd/pro/start.go` helm workdir unchanged (correct use of `os.TempDir()`)
- [ ] All existing tests pass

---

### Phase 5: Migration Logic for Existing User Data

**Goal**: When a user upgrades to a version with XDG support, their existing data at `~/.devsy` should be accessible. Provide automatic migration on first run.

**Files to create**:
| File | Purpose |
|---|---|
| `pkg/config/migrate.go` | Migration logic |
| `pkg/config/migrate_test.go` | Migration tests |

**Migration strategy**:

1. **Detection**: On startup (in `DefaultPathManager()` init or a separate `MigrateIfNeeded()` call), check if:
   - `DEVSY_HOME` is NOT set (legacy mode doesn't need migration)
   - `~/.devsy` exists (old layout)
   - XDG paths do NOT yet have data (avoid re-migration)

2. **Migration actions**:
   - Copy `~/.devsy/config.yaml` → `<ConfigDir>/config.yaml`
   - Move `~/.devsy/contexts/` → `<DataDir>/contexts/`
   - Move `~/.devsy/keys/` → `<CacheDir>/keys/`
   - Leave `~/.devsy` in place as a breadcrumb (create `~/.devsy/.migrated` marker file with timestamp and target paths)

3. **Fallback reads**: If a file is not found at the new XDG path, check `~/.devsy` as a fallback. This handles the case where migration failed or was interrupted.

4. **Legacy `.loft` migration**: If `~/.loft/config.json` exists and `<CacheDir>/platform/config.json` does not, copy it.

5. **No forced migration**: Users can set `DEVSY_HOME=~/.devsy` to opt out permanently.

**Acceptance criteria**:
- [ ] First run after upgrade migrates `~/.devsy` to XDG paths
- [ ] Migration is idempotent — safe to run multiple times
- [ ] `~/.devsy/.migrated` marker prevents re-migration
- [ ] Interrupted migration doesn't lose data (copy-then-mark, not move-then-mark)
- [ ] `DEVSY_HOME` users are not affected
- [ ] `~/.loft/config.json` is migrated to `<CacheDir>/platform/config.json`
- [ ] Tests cover: fresh install (no migration needed), upgrade (migration runs), interrupted migration (re-runs safely), `DEVSY_HOME` set (skipped)

---

## Callsite Inventory

Complete list of callsites that need updating, organized by phase:

### Phase 2 (config.GetConfigDir + provider.Get*Dir)

| Callsite | Current Call |
|---|---|
| `pkg/config/dir.go:13` | `GetConfigDir()` — returns `$DEVSY_HOME` or `~/.devsy` |
| `pkg/config/dir.go:28` | `GetConfigPath()` — returns config file path |
| `pkg/config/config.go:202` | `GetConfigPath()` — loads config |
| `pkg/config/config.go:269` | `GetConfigPath()` — saves config |
| `pkg/provider/dir.go` (10 functions) | All call `config.GetConfigDir()` to build context-relative paths |

### Phase 3 (legacy .loft)

| Callsite | Current Call |
|---|---|
| `pkg/platform/client/client.go:51-58` | `init()` builds `CacheFolder` and `DefaultCacheConfig` from `$DEVSY_CACHE_FOLDER` or `~/.loft` |

### Phase 4 (os.TempDir usages)

| Callsite | File:Line | Current |
|---|---|---|
| Agent cache | `pkg/agent/binary.go:29` | `os.TempDir()/devsy-cache` |
| Provider cache | `pkg/provider/download.go:291` | `os.TempDir()` / `os.UserCacheDir()` |
| Feature cache | `pkg/devcontainer/feature/features.go:381` | `os.TempDir()/devsy/features/<hash>` |
| Daemon PID | `pkg/command/background.go:24-26` | `os.TempDir()/<name>.{pid,lock,streams}` |
| Daemon stop | `pkg/daemon/agent/daemon.go:309` | `os.TempDir()/devsy.daemon.pid` |
| SSH keys | `pkg/ssh/keys.go:85-98` | `~/.devsy/keys` or `os.TempDir()/devsy-ssh` |
| SSH agent | `pkg/ssh/server/agent.go:24` | `os.TempDir()/auth-agent-<id>` |
| Docker cred log | `pkg/dockercredentials/helper.go:166` | `os.TempDir()/<logfile>` |
| Pprof | `cmd/profile.go:26` | `/tmp/pprof_ports` |

### Not migrated (correct usage)

| Callsite | File:Line | Reason |
|---|---|---|
| Helm workdir | `cmd/pro/start.go:1957` | Genuinely temporary working directory |
| Archive temp | `pkg/provider/download.go:579` | `os.CreateTemp` for zip extraction — truly ephemeral |
| Test temp dir | `pkg/devcontainer/config/parse_test.go:35` | Test-only temp dir |

---

## Risk Assessment

| Risk | Mitigation |
|---|---|
| Breaking existing user setups | Phase 5 migration + `DEVSY_HOME` opt-out |
| XDG_RUNTIME_DIR not set (containers, SSH) | Fallback to `os.TempDir()/devsy-<uid>` |
| Permission issues on XDG dirs | Create with `0o700` (XDG spec recommendation) |
| Config/data semantic split confusion | Phase 2 keeps `GetConfigDir()` returning `DataDir()` for safety |
| Partial migration | Copy-then-mark strategy; fallback reads from old paths |
| `background.go` is used inside containers too | Container agent runs will keep using temp dirs (no XDG inside containers); PathManager detects this via absence of home dir |

---

## Open Questions

1. **Should `background.go:StartBackgroundOnce` accept a `PathManager` parameter or use the global?** Recommendation: use global `DefaultPathManager()` — the function is already stateless and package-level.

2. **Should we split `GetConfigDir()` into `GetConfigDir()` (config.yaml only) and `GetDataDir()` (contexts/workspaces) in Phase 2 or defer?** Recommendation: defer the split. Phase 2 keeps `GetConfigDir() = DataDir()` for zero-risk. The semantic split can come as a follow-up once all callsites use `PathManager` directly.

3. **What about running inside containers?** The agent runs inside workspace containers where there's no XDG setup. Recommendation: in container context (detectable via `DEVCONTAINER_ID` env var or absence of user home), fall back to `os.TempDir()` for runtime and `/tmp/devsy` for cache. This matches current behavior.
