# Plan 6: IPC & Frontend Stores

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create Tauri commands for workspace/provider/machine CRUD, a typed TypeScript IPC layer, and Svelte stores that subscribe to daemon events for real-time state.

**Architecture:** Rust side exposes `#[tauri::command]` functions that read from DaemonState or run CLI mutations. TypeScript side has thin typed wrappers around `invoke()` and `listen()`. Svelte stores call commands on mount and subscribe to events for live updates.

**Tech Stack:** Rust (tauri commands), TypeScript, Svelte 5 (runes)

---

### Task 1: Create Rust Tauri commands for workspaces

**Files:**
- Create: `desktop-new/src-tauri/src/commands/mod.rs`
- Create: `desktop-new/src-tauri/src/commands/workspaces.rs`
- Modify: `desktop-new/src-tauri/src/main.rs`

- [ ] **Step 1: Create commands directory and mod.rs**

```bash
mkdir -p desktop-new/src-tauri/src/commands
```

```rust
// commands/mod.rs
pub mod workspaces;
```

- [ ] **Step 2: Create commands/workspaces.rs**

```rust
use crate::daemon::cli::{CliRunner, OutputLine};
use crate::daemon::state::DaemonState;
use crate::daemon::types::Workspace;
use crate::events::{event_names, CommandProgressPayload};
use crate::persistence::audit::AuditLog;
use crate::persistence::logs::LogStore;
use std::sync::Arc;
use tauri::{AppHandle, Emitter, State, Wry};
use tokio::sync::RwLock;

type SharedState = Arc<RwLock<DaemonState>>;

#[tauri::command]
pub async fn workspace_list(
    state: State<'_, SharedState>,
) -> Result<Vec<Workspace>, String> {
    let state = state.read().await;
    Ok(state.workspace_list())
}

#[tauri::command]
pub async fn workspace_up(
    app: AppHandle<Wry>,
    cli: State<'_, Arc<CliRunner>>,
    log_store: State<'_, Arc<LogStore>>,
    audit: State<'_, Arc<AuditLog>>,
    source: String,
    workspace_id: Option<String>,
    provider: Option<String>,
    ide: Option<String>,
) -> Result<String, String> {
    let mut args = vec!["up", &source];

    let ws_id_str;
    if let Some(ref id) = workspace_id {
        ws_id_str = id.clone();
        args.push("--id");
        args.push(&ws_id_str);
    }

    let provider_str;
    if let Some(ref p) = provider {
        provider_str = p.clone();
        args.push("--provider");
        args.push(&provider_str);
    }

    let ide_str;
    if let Some(ref i) = ide {
        ide_str = i.clone();
        args.push("--ide");
        args.push(&ide_str);
    }

    let cmd_id = uuid::Uuid::new_v4().to_string();
    let log_ws_id = workspace_id.as_deref().unwrap_or("unknown");

    // Create log file
    let log_path = log_store
        .create_log_file(log_ws_id, "up")
        .map_err(|e| e.to_string())?;

    // Record audit start
    let cmd_str = format!("devpod {}", args.join(" "));
    let _ = audit.record(
        "workspace_up_started",
        Some(log_ws_id),
        Some(&cmd_str),
        None,
        None,
        "started",
    );

    let start = std::time::Instant::now();

    // Stream the command
    let (tx, mut rx) = tokio::sync::mpsc::channel::<OutputLine>(256);
    let _handle = cli.run_streaming(&args, tx);

    let app_clone = app.clone();
    let cmd_id_clone = cmd_id.clone();
    let log_path_clone = log_path.clone();
    tokio::spawn(async move {
        while let Some(line) = rx.recv().await {
            match &line {
                OutputLine::Stdout(text) | OutputLine::Stderr(text) => {
                    let _ = LogStore::append_log(&log_path_clone, text);
                    let _ = app_clone.emit(
                        event_names::COMMAND_PROGRESS,
                        CommandProgressPayload {
                            id: cmd_id_clone.clone(),
                            status: "running".to_string(),
                            output_line: text.clone(),
                        },
                    );
                }
                OutputLine::Exit(code) => {
                    let status = if *code == 0 { "completed" } else { "failed" };
                    let _ = app_clone.emit(
                        event_names::COMMAND_PROGRESS,
                        CommandProgressPayload {
                            id: cmd_id_clone.clone(),
                            status: status.to_string(),
                            output_line: format!("Exit code: {}", code),
                        },
                    );
                }
            }
        }
    });

    let duration = start.elapsed().as_millis() as i64;
    let _ = audit.record(
        "workspace_up_completed",
        Some(log_ws_id),
        Some(&cmd_str),
        None,
        Some(duration),
        "success",
    );

    Ok(cmd_id)
}

#[tauri::command]
pub async fn workspace_stop(
    cli: State<'_, Arc<CliRunner>>,
    audit: State<'_, Arc<AuditLog>>,
    workspace_id: String,
) -> Result<(), String> {
    let args = ["stop", &workspace_id];
    let cmd_str = format!("devpod {}", args.join(" "));

    let start = std::time::Instant::now();
    cli.run_raw(&args).await.map_err(|e| e.to_string())?;
    let duration = start.elapsed().as_millis() as i64;

    let _ = audit.record(
        "workspace_stopped",
        Some(&workspace_id),
        Some(&cmd_str),
        None,
        Some(duration),
        "success",
    );

    Ok(())
}

#[tauri::command]
pub async fn workspace_delete(
    cli: State<'_, Arc<CliRunner>>,
    audit: State<'_, Arc<AuditLog>>,
    workspace_id: String,
) -> Result<(), String> {
    let args = ["delete", &workspace_id, "--force"];
    let cmd_str = format!("devpod {}", args.join(" "));

    let start = std::time::Instant::now();
    cli.run_raw(&args).await.map_err(|e| e.to_string())?;
    let duration = start.elapsed().as_millis() as i64;

    let _ = audit.record(
        "workspace_deleted",
        Some(&workspace_id),
        Some(&cmd_str),
        None,
        Some(duration),
        "success",
    );

    Ok(())
}

#[tauri::command]
pub async fn workspace_rebuild(
    cli: State<'_, Arc<CliRunner>>,
    audit: State<'_, Arc<AuditLog>>,
    workspace_id: String,
) -> Result<(), String> {
    let args = ["up", &workspace_id, "--recreate"];
    let cmd_str = format!("devpod {}", args.join(" "));

    let start = std::time::Instant::now();
    cli.run_raw(&args).await.map_err(|e| e.to_string())?;
    let duration = start.elapsed().as_millis() as i64;

    let _ = audit.record(
        "workspace_rebuilt",
        Some(&workspace_id),
        Some(&cmd_str),
        None,
        Some(duration),
        "success",
    );

    Ok(())
}

#[tauri::command]
pub async fn workspace_logs(
    log_store: State<'_, Arc<LogStore>>,
    workspace_id: String,
) -> Result<Vec<crate::persistence::logs::LogEntry>, String> {
    log_store.list_logs(&workspace_id).map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn workspace_log_content(
    path: String,
) -> Result<String, String> {
    LogStore::read_log(std::path::Path::new(&path)).map_err(|e| e.to_string())
}
```

- [ ] **Step 3: Add uuid dependency to Cargo.toml**

```toml
uuid = { version = "1.0", features = ["v4"] }
```

- [ ] **Step 4: Register commands module and commands in main.rs**

Add `mod commands;` and register the command handlers:

```rust
mod commands;
mod daemon;
mod events;
mod persistence;
```

In the builder, add `.invoke_handler()`:

```rust
        .invoke_handler(tauri::generate_handler![
            commands::workspaces::workspace_list,
            commands::workspaces::workspace_up,
            commands::workspaces::workspace_stop,
            commands::workspaces::workspace_delete,
            commands::workspaces::workspace_rebuild,
            commands::workspaces::workspace_logs,
            commands::workspaces::workspace_log_content,
        ])
```

Also manage the CliRunner as shared state:

```rust
            app.manage(cli.clone());
```

(Add this inside the `Ok(binary_path)` match arm, after creating `cli`.)

- [ ] **Step 5: Verify it compiles**

```bash
cd desktop-new/src-tauri
cargo check
```

Expected: Compiles without errors.

- [ ] **Step 6: Commit**

```bash
git add desktop-new/src-tauri/
git commit -m "feat(ipc): add Tauri commands for workspace CRUD"
```

---

### Task 2: Create Rust commands for providers and machines

**Files:**
- Create: `desktop-new/src-tauri/src/commands/providers.rs`
- Create: `desktop-new/src-tauri/src/commands/machines.rs`
- Modify: `desktop-new/src-tauri/src/commands/mod.rs`
- Modify: `desktop-new/src-tauri/src/main.rs`

- [ ] **Step 1: Create commands/providers.rs**

```rust
use crate::daemon::cli::CliRunner;
use crate::daemon::state::DaemonState;
use crate::daemon::types::Provider;
use crate::persistence::audit::AuditLog;
use std::sync::Arc;
use tauri::State;
use tokio::sync::RwLock;

type SharedState = Arc<RwLock<DaemonState>>;

#[tauri::command]
pub async fn provider_list(
    state: State<'_, SharedState>,
) -> Result<Vec<Provider>, String> {
    let state = state.read().await;
    Ok(state.provider_list())
}

#[tauri::command]
pub async fn provider_add(
    cli: State<'_, Arc<CliRunner>>,
    audit: State<'_, Arc<AuditLog>>,
    name: String,
) -> Result<(), String> {
    let args = ["provider", "add", &name];
    let cmd_str = format!("devpod {}", args.join(" "));

    let start = std::time::Instant::now();
    cli.run_raw(&args).await.map_err(|e| e.to_string())?;
    let duration = start.elapsed().as_millis() as i64;

    let _ = audit.record("provider_added", Some(&name), Some(&cmd_str), None, Some(duration), "success");
    Ok(())
}

#[tauri::command]
pub async fn provider_delete(
    cli: State<'_, Arc<CliRunner>>,
    audit: State<'_, Arc<AuditLog>>,
    name: String,
) -> Result<(), String> {
    let args = ["provider", "delete", &name];
    let cmd_str = format!("devpod {}", args.join(" "));

    let start = std::time::Instant::now();
    cli.run_raw(&args).await.map_err(|e| e.to_string())?;
    let duration = start.elapsed().as_millis() as i64;

    let _ = audit.record("provider_deleted", Some(&name), Some(&cmd_str), None, Some(duration), "success");
    Ok(())
}

#[tauri::command]
pub async fn provider_use(
    cli: State<'_, Arc<CliRunner>>,
    audit: State<'_, Arc<AuditLog>>,
    name: String,
) -> Result<(), String> {
    let args = ["provider", "use", &name];
    let cmd_str = format!("devpod {}", args.join(" "));

    cli.run_raw(&args).await.map_err(|e| e.to_string())?;
    let _ = audit.record("provider_use", Some(&name), Some(&cmd_str), None, None, "success");
    Ok(())
}

#[tauri::command]
pub async fn provider_update(
    cli: State<'_, Arc<CliRunner>>,
    audit: State<'_, Arc<AuditLog>>,
    name: String,
) -> Result<(), String> {
    let args = ["provider", "update", &name];
    let cmd_str = format!("devpod {}", args.join(" "));

    cli.run_raw(&args).await.map_err(|e| e.to_string())?;
    let _ = audit.record("provider_updated", Some(&name), Some(&cmd_str), None, None, "success");
    Ok(())
}

#[tauri::command]
pub async fn provider_options(
    cli: State<'_, Arc<CliRunner>>,
    name: String,
) -> Result<serde_json::Value, String> {
    cli.run::<serde_json::Value>(&["provider", "options", &name])
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn provider_set_options(
    cli: State<'_, Arc<CliRunner>>,
    audit: State<'_, Arc<AuditLog>>,
    name: String,
    options: Vec<String>,
) -> Result<(), String> {
    let mut args: Vec<&str> = vec!["provider", "set-options", &name];
    let option_refs: Vec<&str> = options.iter().map(|s| s.as_str()).collect();
    args.extend(option_refs);

    let cmd_str = format!("devpod {}", args.join(" "));
    cli.run_raw(&args).await.map_err(|e| e.to_string())?;
    let _ = audit.record("provider_options_set", Some(&name), Some(&cmd_str), None, None, "success");
    Ok(())
}
```

- [ ] **Step 2: Create commands/machines.rs**

```rust
use crate::daemon::cli::CliRunner;
use crate::daemon::state::DaemonState;
use crate::daemon::types::Machine;
use crate::persistence::audit::AuditLog;
use std::sync::Arc;
use tauri::State;
use tokio::sync::RwLock;

type SharedState = Arc<RwLock<DaemonState>>;

#[tauri::command]
pub async fn machine_list(
    state: State<'_, SharedState>,
) -> Result<Vec<Machine>, String> {
    let state = state.read().await;
    Ok(state.machine_list())
}

#[tauri::command]
pub async fn machine_create(
    cli: State<'_, Arc<CliRunner>>,
    audit: State<'_, Arc<AuditLog>>,
    name: String,
    provider: String,
) -> Result<(), String> {
    let args = ["machine", "create", &name, "--provider", &provider];
    let cmd_str = format!("devpod {}", args.join(" "));

    let start = std::time::Instant::now();
    cli.run_raw(&args).await.map_err(|e| e.to_string())?;
    let duration = start.elapsed().as_millis() as i64;

    let _ = audit.record("machine_created", Some(&name), Some(&cmd_str), None, Some(duration), "success");
    Ok(())
}

#[tauri::command]
pub async fn machine_delete(
    cli: State<'_, Arc<CliRunner>>,
    audit: State<'_, Arc<AuditLog>>,
    name: String,
) -> Result<(), String> {
    let args = ["machine", "delete", &name];
    let cmd_str = format!("devpod {}", args.join(" "));

    cli.run_raw(&args).await.map_err(|e| e.to_string())?;
    let _ = audit.record("machine_deleted", Some(&name), Some(&cmd_str), None, None, "success");
    Ok(())
}

#[tauri::command]
pub async fn machine_start(
    cli: State<'_, Arc<CliRunner>>,
    audit: State<'_, Arc<AuditLog>>,
    name: String,
) -> Result<(), String> {
    let args = ["machine", "start", &name];
    let cmd_str = format!("devpod {}", args.join(" "));

    cli.run_raw(&args).await.map_err(|e| e.to_string())?;
    let _ = audit.record("machine_started", Some(&name), Some(&cmd_str), None, None, "success");
    Ok(())
}

#[tauri::command]
pub async fn machine_stop(
    cli: State<'_, Arc<CliRunner>>,
    audit: State<'_, Arc<AuditLog>>,
    name: String,
) -> Result<(), String> {
    let args = ["machine", "stop", &name];
    let cmd_str = format!("devpod {}", args.join(" "));

    cli.run_raw(&args).await.map_err(|e| e.to_string())?;
    let _ = audit.record("machine_stopped", Some(&name), Some(&cmd_str), None, None, "success");
    Ok(())
}

#[tauri::command]
pub async fn machine_status(
    cli: State<'_, Arc<CliRunner>>,
    name: String,
) -> Result<String, String> {
    cli.run_raw(&["machine", "status", &name, "--output", "json"])
        .await
        .map_err(|e| e.to_string())
}
```

- [ ] **Step 3: Update commands/mod.rs**

```rust
pub mod machines;
pub mod providers;
pub mod workspaces;
```

- [ ] **Step 4: Register all commands in main.rs invoke_handler**

```rust
        .invoke_handler(tauri::generate_handler![
            // Workspaces
            commands::workspaces::workspace_list,
            commands::workspaces::workspace_up,
            commands::workspaces::workspace_stop,
            commands::workspaces::workspace_delete,
            commands::workspaces::workspace_rebuild,
            commands::workspaces::workspace_logs,
            commands::workspaces::workspace_log_content,
            // Providers
            commands::providers::provider_list,
            commands::providers::provider_add,
            commands::providers::provider_delete,
            commands::providers::provider_use,
            commands::providers::provider_update,
            commands::providers::provider_options,
            commands::providers::provider_set_options,
            // Machines
            commands::machines::machine_list,
            commands::machines::machine_create,
            commands::machines::machine_delete,
            commands::machines::machine_start,
            commands::machines::machine_stop,
            commands::machines::machine_status,
        ])
```

- [ ] **Step 5: Verify it compiles**

```bash
cd desktop-new/src-tauri
cargo check
```

Expected: Compiles without errors.

- [ ] **Step 6: Commit**

```bash
git add desktop-new/src-tauri/
git commit -m "feat(ipc): add Tauri commands for providers and machines"
```

---

### Task 3: Create TypeScript IPC layer

**Files:**
- Create: `desktop-new/src/lib/ipc/commands.ts`
- Create: `desktop-new/src/lib/ipc/events.ts`
- Create: `desktop-new/src/lib/types/index.ts`

- [ ] **Step 1: Create lib/types/index.ts with shared types**

```ts
// Types matching Rust daemon types

export interface Workspace {
  id: string;
  uid: string;
  picture: string;
  provider: WorkspaceProviderConfig;
  machine: WorkspaceMachineConfig;
  ide: WorkspaceIDEConfig;
  source: WorkspaceSource;
  devContainerImage: string;
  devContainerPath: string;
  creationTimestamp: string;
  lastUsed: string;
  context: string;
  imported: boolean;
  sshConfigPath: string;
}

export interface WorkspaceProviderConfig {
  name: string;
  options: Record<string, OptionValue>;
}

export interface WorkspaceMachineConfig {
  machineId: string;
  autoDelete: boolean;
}

export interface WorkspaceIDEConfig {
  name: string;
  options: Record<string, OptionValue>;
}

export interface WorkspaceSource {
  gitRepository: string;
  gitBranch: string;
  gitCommit: string;
  gitSubDir: string;
  localFolder: string;
  image: string;
  container: string;
}

export interface OptionValue {
  value: string;
}

export interface Provider {
  name: string;
  version: string;
  icon: string;
  description: string;
  source: ProviderSource;
  options: Record<string, ProviderOption>;
  optionGroups: ProviderOptionGroup[];
  isDefault: boolean;
  state: string;
}

export interface ProviderSource {
  github: string;
  file: string;
  url: string;
  raw: string;
  internal: boolean;
}

export interface ProviderOption {
  value: string;
  description: string;
  required: boolean;
  default: string;
  type: string;
}

export interface ProviderOptionGroup {
  name: string;
  options: string[];
  defaultVisible: boolean;
}

export interface Machine {
  id: string;
  provider: MachineProviderConfig;
  creationTimestamp: string;
  context: string;
}

export interface MachineProviderConfig {
  name: string;
  options: Record<string, OptionValue>;
}

export interface Context {
  name: string;
  options: Record<string, string>;
}

export interface LogEntry {
  workspace_id: string;
  command: string;
  timestamp: string;
  file_path: string;
}

export interface CommandProgress {
  id: string;
  status: string;
  output_line: string;
}
```

- [ ] **Step 2: Create lib/ipc/commands.ts**

```ts
import { invoke } from "@tauri-apps/api/core";
import type {
  Workspace,
  Provider,
  Machine,
  LogEntry,
} from "$lib/types";

// --- Workspaces ---

export async function workspaceList(): Promise<Workspace[]> {
  return invoke("workspace_list");
}

export async function workspaceUp(params: {
  source: string;
  workspaceId?: string;
  provider?: string;
  ide?: string;
}): Promise<string> {
  return invoke("workspace_up", {
    source: params.source,
    workspaceId: params.workspaceId ?? null,
    provider: params.provider ?? null,
    ide: params.ide ?? null,
  });
}

export async function workspaceStop(workspaceId: string): Promise<void> {
  return invoke("workspace_stop", { workspaceId });
}

export async function workspaceDelete(workspaceId: string): Promise<void> {
  return invoke("workspace_delete", { workspaceId });
}

export async function workspaceRebuild(workspaceId: string): Promise<void> {
  return invoke("workspace_rebuild", { workspaceId });
}

export async function workspaceLogs(
  workspaceId: string,
): Promise<LogEntry[]> {
  return invoke("workspace_logs", { workspaceId });
}

export async function workspaceLogContent(path: string): Promise<string> {
  return invoke("workspace_log_content", { path });
}

// --- Providers ---

export async function providerList(): Promise<Provider[]> {
  return invoke("provider_list");
}

export async function providerAdd(name: string): Promise<void> {
  return invoke("provider_add", { name });
}

export async function providerDelete(name: string): Promise<void> {
  return invoke("provider_delete", { name });
}

export async function providerUse(name: string): Promise<void> {
  return invoke("provider_use", { name });
}

export async function providerUpdate(name: string): Promise<void> {
  return invoke("provider_update", { name });
}

export async function providerOptions(name: string): Promise<unknown> {
  return invoke("provider_options", { name });
}

export async function providerSetOptions(
  name: string,
  options: string[],
): Promise<void> {
  return invoke("provider_set_options", { name, options });
}

// --- Machines ---

export async function machineList(): Promise<Machine[]> {
  return invoke("machine_list");
}

export async function machineCreate(
  name: string,
  provider: string,
): Promise<void> {
  return invoke("machine_create", { name, provider });
}

export async function machineDelete(name: string): Promise<void> {
  return invoke("machine_delete", { name });
}

export async function machineStart(name: string): Promise<void> {
  return invoke("machine_start", { name });
}

export async function machineStop(name: string): Promise<void> {
  return invoke("machine_stop", { name });
}

export async function machineStatus(name: string): Promise<string> {
  return invoke("machine_status", { name });
}
```

- [ ] **Step 3: Create lib/ipc/events.ts**

```ts
import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import type {
  Workspace,
  Provider,
  Machine,
  Context,
  CommandProgress,
} from "$lib/types";

export const EVENT_NAMES = {
  WORKSPACES_CHANGED: "workspaces:changed",
  PROVIDERS_CHANGED: "providers:changed",
  MACHINES_CHANGED: "machines:changed",
  CONTEXTS_CHANGED: "contexts:changed",
  COMMAND_PROGRESS: "command:progress",
  DAEMON_STATUS: "daemon:status",
} as const;

interface WorkspacesPayload {
  workspaces: Workspace[];
}

interface ProvidersPayload {
  providers: Provider[];
}

interface MachinesPayload {
  machines: Machine[];
}

interface ContextsPayload {
  contexts: Context[];
  active: string;
}

export function onWorkspacesChanged(
  callback: (workspaces: Workspace[]) => void,
): Promise<UnlistenFn> {
  return listen<WorkspacesPayload>(
    EVENT_NAMES.WORKSPACES_CHANGED,
    (event) => callback(event.payload.workspaces),
  );
}

export function onProvidersChanged(
  callback: (providers: Provider[]) => void,
): Promise<UnlistenFn> {
  return listen<ProvidersPayload>(
    EVENT_NAMES.PROVIDERS_CHANGED,
    (event) => callback(event.payload.providers),
  );
}

export function onMachinesChanged(
  callback: (machines: Machine[]) => void,
): Promise<UnlistenFn> {
  return listen<MachinesPayload>(
    EVENT_NAMES.MACHINES_CHANGED,
    (event) => callback(event.payload.machines),
  );
}

export function onContextsChanged(
  callback: (contexts: Context[], active: string) => void,
): Promise<UnlistenFn> {
  return listen<ContextsPayload>(
    EVENT_NAMES.CONTEXTS_CHANGED,
    (event) => callback(event.payload.contexts, event.payload.active),
  );
}

export function onCommandProgress(
  callback: (progress: CommandProgress) => void,
): Promise<UnlistenFn> {
  return listen<CommandProgress>(
    EVENT_NAMES.COMMAND_PROGRESS,
    (event) => callback(event.payload),
  );
}
```

- [ ] **Step 4: Verify frontend builds**

```bash
cd desktop-new
npm run build
```

Expected: Build succeeds.

- [ ] **Step 5: Commit**

```bash
git add desktop-new/src/lib/
git commit -m "feat(ipc): create typed TypeScript IPC layer for commands and events"
```

---

### Task 4: Create Svelte stores

**Files:**
- Create: `desktop-new/src/lib/stores/workspaces.ts`
- Create: `desktop-new/src/lib/stores/providers.ts`
- Create: `desktop-new/src/lib/stores/machines.ts`

- [ ] **Step 1: Create lib/stores/workspaces.ts**

```ts
import { writable } from "svelte/store";
import type { Workspace } from "$lib/types";
import { workspaceList } from "$lib/ipc/commands";
import { onWorkspacesChanged } from "$lib/ipc/events";
import type { UnlistenFn } from "@tauri-apps/api/event";

function createWorkspacesStore() {
  const { subscribe, set } = writable<Workspace[]>([]);
  let unlisten: UnlistenFn | null = null;

  return {
    subscribe,
    async init() {
      // Fetch initial state
      try {
        const ws = await workspaceList();
        set(ws);
      } catch (e) {
        console.error("Failed to fetch workspaces:", e);
      }

      // Subscribe to daemon events
      unlisten = await onWorkspacesChanged((workspaces) => {
        set(workspaces);
      });
    },
    destroy() {
      if (unlisten) {
        unlisten();
        unlisten = null;
      }
    },
  };
}

export const workspaces = createWorkspacesStore();
```

- [ ] **Step 2: Create lib/stores/providers.ts**

```ts
import { writable } from "svelte/store";
import type { Provider } from "$lib/types";
import { providerList } from "$lib/ipc/commands";
import { onProvidersChanged } from "$lib/ipc/events";
import type { UnlistenFn } from "@tauri-apps/api/event";

function createProvidersStore() {
  const { subscribe, set } = writable<Provider[]>([]);
  let unlisten: UnlistenFn | null = null;

  return {
    subscribe,
    async init() {
      try {
        const providers = await providerList();
        set(providers);
      } catch (e) {
        console.error("Failed to fetch providers:", e);
      }

      unlisten = await onProvidersChanged((providers) => {
        set(providers);
      });
    },
    destroy() {
      if (unlisten) {
        unlisten();
        unlisten = null;
      }
    },
  };
}

export const providers = createProvidersStore();
```

- [ ] **Step 3: Create lib/stores/machines.ts**

```ts
import { writable } from "svelte/store";
import type { Machine } from "$lib/types";
import { machineList } from "$lib/ipc/commands";
import { onMachinesChanged } from "$lib/ipc/events";
import type { UnlistenFn } from "@tauri-apps/api/event";

function createMachinesStore() {
  const { subscribe, set } = writable<Machine[]>([]);
  let unlisten: UnlistenFn | null = null;

  return {
    subscribe,
    async init() {
      try {
        const machines = await machineList();
        set(machines);
      } catch (e) {
        console.error("Failed to fetch machines:", e);
      }

      unlisten = await onMachinesChanged((machines) => {
        set(machines);
      });
    },
    destroy() {
      if (unlisten) {
        unlisten();
        unlisten = null;
      }
    },
  };
}

export const machines = createMachinesStore();
```

- [ ] **Step 4: Verify frontend builds**

```bash
cd desktop-new
npm run build
```

Expected: Build succeeds.

- [ ] **Step 5: Commit**

```bash
git add desktop-new/src/lib/stores/
git commit -m "feat(stores): create Svelte stores with daemon event subscriptions"
```
