# Plan 4: Daemon Watcher

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a background tokio task that polls the devpod CLI for state changes and watches `~/.devpod/` for file changes, emitting Tauri events when state updates.

**Architecture:** A `Watcher` struct spawns two background tasks: (1) a polling loop that runs CLI list commands on a configurable interval and updates DaemonState, (2) a filesystem watcher via the `notify` crate that triggers an immediate poll when config files change. Both emit typed Tauri events on state change.

**Tech Stack:** Rust, tokio, notify (filesystem watching), tauri events

---

### Task 1: Define event types

**Files:**
- Create: `desktop-new/src-tauri/src/events.rs`
- Modify: `desktop-new/src-tauri/src/main.rs`

- [ ] **Step 1: Create events.rs with typed event constants**

```rust
use crate::daemon::types::{Machine, Provider, Workspace, Context};
use serde::{Deserialize, Serialize};

/// Event name constants for Tauri event system.
pub mod event_names {
    pub const WORKSPACES_CHANGED: &str = "workspaces:changed";
    pub const PROVIDERS_CHANGED: &str = "providers:changed";
    pub const MACHINES_CHANGED: &str = "machines:changed";
    pub const CONTEXTS_CHANGED: &str = "contexts:changed";
    pub const COMMAND_PROGRESS: &str = "command:progress";
    pub const DAEMON_STATUS: &str = "daemon:status";
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WorkspacesPayload {
    pub workspaces: Vec<Workspace>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ProvidersPayload {
    pub providers: Vec<Provider>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MachinesPayload {
    pub machines: Vec<Machine>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ContextsPayload {
    pub contexts: Vec<Context>,
    pub active: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CommandProgressPayload {
    pub id: String,
    pub status: String,
    pub output_line: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum DaemonStatusPayload {
    Starting,
    Running,
    Error(String),
}
```

- [ ] **Step 2: Register events module in main.rs**

Add `mod events;` to the top of `main.rs` (after `mod daemon;`):

```rust
mod daemon;
mod events;
```

- [ ] **Step 3: Verify it compiles**

```bash
cd desktop-new/src-tauri
cargo check
```

Expected: Compiles without errors.

- [ ] **Step 4: Commit**

```bash
git add desktop-new/src-tauri/src/events.rs desktop-new/src-tauri/src/main.rs
git commit -m "feat(daemon): define typed Tauri event payloads"
```

---

### Task 2: Build the polling watcher

**Files:**
- Create: `desktop-new/src-tauri/src/daemon/watcher.rs`
- Modify: `desktop-new/src-tauri/src/daemon/mod.rs`

- [ ] **Step 1: Create daemon/watcher.rs with the Watcher struct**

```rust
use crate::daemon::cli::CliRunner;
use crate::daemon::state::DaemonState;
use crate::daemon::types::{Machine, Provider, Workspace};
use crate::events::{
    event_names, MachinesPayload, ProvidersPayload, WorkspacesPayload, DaemonStatusPayload,
};
use log::{debug, error, info};
use std::sync::Arc;
use std::time::Duration;
use tauri::{AppHandle, Emitter, Wry};
use tokio::sync::RwLock;

pub struct Watcher {
    cli: Arc<CliRunner>,
    state: Arc<RwLock<DaemonState>>,
    poll_interval: Duration,
    app_handle: AppHandle<Wry>,
}

impl Watcher {
    pub fn new(
        cli: Arc<CliRunner>,
        state: Arc<RwLock<DaemonState>>,
        poll_interval: Duration,
        app_handle: AppHandle<Wry>,
    ) -> Self {
        Self {
            cli,
            state,
            poll_interval,
            app_handle,
        }
    }

    /// Start the polling loop. Returns a JoinHandle for the background task.
    pub fn start_polling(self) -> tokio::task::JoinHandle<()> {
        tokio::spawn(async move {
            info!("Daemon watcher: starting poll loop (interval: {:?})", self.poll_interval);
            let _ = self.app_handle.emit(event_names::DAEMON_STATUS, DaemonStatusPayload::Running);

            loop {
                self.poll_once().await;
                tokio::time::sleep(self.poll_interval).await;
            }
        })
    }

    async fn poll_once(&self) {
        self.poll_workspaces().await;
        self.poll_providers().await;
        self.poll_machines().await;
    }

    async fn poll_workspaces(&self) {
        match self.cli.run::<Vec<Workspace>>(&["list", "--skip-pro"]).await {
            Ok(workspaces) => {
                let mut state = self.state.write().await;
                if state.update_workspaces(workspaces) {
                    let payload = WorkspacesPayload {
                        workspaces: state.workspace_list(),
                    };
                    debug!("Daemon watcher: workspaces changed, emitting event");
                    let _ = self.app_handle.emit(event_names::WORKSPACES_CHANGED, payload);
                }
            }
            Err(e) => {
                error!("Daemon watcher: failed to poll workspaces: {}", e);
            }
        }
    }

    async fn poll_providers(&self) {
        match self.cli.run::<Vec<Provider>>(&["provider", "list"]).await {
            Ok(providers) => {
                let mut state = self.state.write().await;
                if state.update_providers(providers) {
                    let payload = ProvidersPayload {
                        providers: state.provider_list(),
                    };
                    debug!("Daemon watcher: providers changed, emitting event");
                    let _ = self.app_handle.emit(event_names::PROVIDERS_CHANGED, payload);
                }
            }
            Err(e) => {
                error!("Daemon watcher: failed to poll providers: {}", e);
            }
        }
    }

    async fn poll_machines(&self) {
        match self.cli.run::<Vec<Machine>>(&["machine", "list"]).await {
            Ok(machines) => {
                let mut state = self.state.write().await;
                if state.update_machines(machines) {
                    let payload = MachinesPayload {
                        machines: state.machine_list(),
                    };
                    debug!("Daemon watcher: machines changed, emitting event");
                    let _ = self.app_handle.emit(event_names::MACHINES_CHANGED, payload);
                }
            }
            Err(e) => {
                error!("Daemon watcher: failed to poll machines: {}", e);
            }
        }
    }
}
```

- [ ] **Step 2: Register watcher module**

Update `daemon/mod.rs`:

```rust
pub mod cli;
pub mod state;
pub mod types;
pub mod watcher;
```

- [ ] **Step 3: Verify it compiles**

```bash
cd desktop-new/src-tauri
cargo check
```

Expected: Compiles without errors.

- [ ] **Step 4: Commit**

```bash
git add desktop-new/src-tauri/src/daemon/
git commit -m "feat(daemon): implement polling watcher with Tauri event emission"
```

---

### Task 3: Add filesystem watcher

**Files:**
- Modify: `desktop-new/src-tauri/src/daemon/watcher.rs`
- Modify: `desktop-new/src-tauri/Cargo.toml`

- [ ] **Step 1: Add notify crate to Cargo.toml**

Add to `[dependencies]`:

```toml
notify = "7.0"
dirs = "5.0"
```

- [ ] **Step 2: Add filesystem watcher to Watcher**

Add a new method to the `Watcher` impl in `daemon/watcher.rs`. Add these imports at the top:

```rust
use notify::{Watcher as FsWatcher, RecommendedWatcher, RecursiveMode, Event};
use tokio::sync::mpsc;
```

Add method:

```rust
    /// Start watching ~/.devpod/ for config file changes.
    /// Triggers an immediate poll when changes are detected.
    pub fn start_fs_watcher(
        cli: Arc<CliRunner>,
        state: Arc<RwLock<DaemonState>>,
        app_handle: AppHandle<Wry>,
    ) -> Option<tokio::task::JoinHandle<()>> {
        let devpod_dir = dirs::home_dir()?.join(".devpod");
        if !devpod_dir.exists() {
            info!("Daemon watcher: ~/.devpod/ not found, skipping fs watcher");
            return None;
        }

        let (tx, mut rx) = mpsc::channel::<()>(16);

        // notify uses std::sync, so we bridge to async via a channel
        let mut fs_watcher = RecommendedWatcher::new(
            move |res: Result<Event, notify::Error>| {
                if let Ok(event) = res {
                    // Only trigger on create/modify/remove events
                    if event.kind.is_create() || event.kind.is_modify() || event.kind.is_remove() {
                        let _ = tx.blocking_send(());
                    }
                }
            },
            notify::Config::default(),
        ).ok()?;

        fs_watcher.watch(&devpod_dir, RecursiveMode::Recursive).ok()?;
        info!("Daemon watcher: watching {:?} for file changes", devpod_dir);

        let handle = tokio::spawn(async move {
            // Keep the fs_watcher alive by holding it in scope
            let _watcher = fs_watcher;

            // Debounce: wait 500ms after last event before polling
            loop {
                if rx.recv().await.is_none() {
                    break;
                }
                // Drain any buffered events
                tokio::time::sleep(Duration::from_millis(500)).await;
                while rx.try_recv().is_ok() {}

                debug!("Daemon watcher: fs change detected, triggering poll");
                // Create a temporary watcher-like struct to reuse poll logic
                let temp = Watcher::new(
                    cli.clone(),
                    state.clone(),
                    Duration::from_secs(0), // unused
                    app_handle.clone(),
                );
                temp.poll_once().await;
            }
        });

        Some(handle)
    }
```

- [ ] **Step 3: Verify it compiles**

```bash
cd desktop-new/src-tauri
cargo check
```

Expected: Compiles without errors.

- [ ] **Step 4: Commit**

```bash
git add desktop-new/src-tauri/
git commit -m "feat(daemon): add filesystem watcher for ~/.devpod/ changes"
```

---

### Task 4: Wire watcher into Tauri setup

**Files:**
- Modify: `desktop-new/src-tauri/src/main.rs`

- [ ] **Step 1: Start watcher in Tauri setup hook**

Update `main.rs`:

```rust
#![cfg_attr(
    all(not(debug_assertions), target_os = "windows"),
    windows_subsystem = "windows"
)]

mod daemon;
mod events;

use daemon::cli::{resolve_binary_path, CliRunner};
use daemon::state::DaemonState;
use daemon::watcher::Watcher;
use log::{error, info};
use std::sync::Arc;
use std::time::Duration;
use tauri::Manager;
use tokio::sync::RwLock;

pub type SharedState = Arc<RwLock<DaemonState>>;

fn main() {
    let state: SharedState = Arc::new(RwLock::new(DaemonState::new()));

    tauri::Builder::default()
        .plugin(tauri_plugin_log::Builder::new().build())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_os::init())
        .plugin(tauri_plugin_process::init())
        .plugin(tauri_plugin_fs::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_clipboard_manager::init())
        .plugin(tauri_plugin_opener::init())
        .manage(state.clone())
        .setup(move |app| {
            let app_handle = app.handle().clone();

            // Resolve devpod binary
            match resolve_binary_path(None) {
                Ok(binary_path) => {
                    info!("Found devpod binary at: {:?}", binary_path);
                    let cli = Arc::new(CliRunner::new(binary_path).unwrap());

                    // Start polling watcher
                    let watcher = Watcher::new(
                        cli.clone(),
                        state.clone(),
                        Duration::from_secs(3),
                        app_handle.clone(),
                    );
                    watcher.start_polling();

                    // Start filesystem watcher
                    Watcher::start_fs_watcher(
                        cli.clone(),
                        state.clone(),
                        app_handle.clone(),
                    );
                }
                Err(e) => {
                    error!("Failed to find devpod binary: {}", e);
                }
            }

            let window = app.get_webview_window("main").unwrap();
            window.show().unwrap();
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
```

- [ ] **Step 2: Verify it compiles**

```bash
cd desktop-new/src-tauri
cargo check
```

Expected: Compiles without errors.

- [ ] **Step 3: Commit**

```bash
git add desktop-new/src-tauri/src/main.rs
git commit -m "feat(daemon): wire watcher into Tauri setup"
```
