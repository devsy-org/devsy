# Plan 3: Daemon State Store

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the in-memory state store that holds workspace, provider, and machine data, supports diffing for change detection, and is shared across Tauri commands via `Arc<RwLock<>>`.

**Architecture:** Rust structs mirror the devpod CLI JSON output. A `DaemonState` struct holds `HashMap`s keyed by ID. A `diff()` method compares old vs new state and returns typed change sets. The state is managed via Tauri's `manage()` API.

**Tech Stack:** Rust, serde, tokio (RwLock), tauri

---

### Task 1: Define workspace, provider, and machine types

**Files:**
- Create: `desktop-new/src-tauri/src/daemon/types.rs`
- Modify: `desktop-new/src-tauri/src/daemon/mod.rs`

- [ ] **Step 1: Create daemon/types.rs with Workspace struct**

These types mirror the Go structs from `pkg/provider/workspace.go` and the JSON output of `devpod list --output json`.

```rust
use serde::{Deserialize, Serialize};
use std::collections::HashMap;

/// Mirrors pkg/provider/workspace.go Workspace struct.
/// Fields match the JSON keys from `devpod list --output json`.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Workspace {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub uid: String,
    #[serde(default)]
    pub picture: String,
    #[serde(default)]
    pub provider: WorkspaceProviderConfig,
    #[serde(default)]
    pub machine: WorkspaceMachineConfig,
    #[serde(default)]
    pub ide: WorkspaceIDEConfig,
    #[serde(default)]
    pub source: WorkspaceSource,
    #[serde(default)]
    pub dev_container_image: String,
    #[serde(default)]
    pub dev_container_path: String,
    #[serde(default)]
    pub creation_timestamp: String,
    #[serde(default, rename = "lastUsed")]
    pub last_used_timestamp: String,
    #[serde(default)]
    pub context: String,
    #[serde(default)]
    pub imported: bool,
    #[serde(default)]
    pub ssh_config_path: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Default)]
#[serde(rename_all = "camelCase")]
pub struct WorkspaceProviderConfig {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub options: HashMap<String, OptionValue>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Default)]
#[serde(rename_all = "camelCase")]
pub struct WorkspaceMachineConfig {
    #[serde(default, rename = "machineId")]
    pub id: String,
    #[serde(default)]
    pub auto_delete: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Default)]
#[serde(rename_all = "camelCase")]
pub struct WorkspaceIDEConfig {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub options: HashMap<String, OptionValue>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Default)]
#[serde(rename_all = "camelCase")]
pub struct WorkspaceSource {
    #[serde(default)]
    pub git_repository: String,
    #[serde(default)]
    pub git_branch: String,
    #[serde(default)]
    pub git_commit: String,
    #[serde(default, rename = "gitSubDir")]
    pub git_sub_path: String,
    #[serde(default)]
    pub local_folder: String,
    #[serde(default)]
    pub image: String,
    #[serde(default)]
    pub container: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Default)]
pub struct OptionValue {
    #[serde(default)]
    pub value: String,
}
```

- [ ] **Step 2: Add Provider and Machine structs to types.rs**

Append to `daemon/types.rs`:

```rust
/// Mirrors the JSON output from `devpod provider list --output json`.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Provider {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub version: String,
    #[serde(default)]
    pub icon: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub source: ProviderSource,
    #[serde(default)]
    pub options: HashMap<String, ProviderOption>,
    #[serde(default)]
    pub option_groups: Vec<ProviderOptionGroup>,
    #[serde(default, rename = "isDefault")]
    pub is_default: bool,
    #[serde(default)]
    pub state: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Default)]
#[serde(rename_all = "camelCase")]
pub struct ProviderSource {
    #[serde(default)]
    pub github: String,
    #[serde(default)]
    pub file: String,
    #[serde(default)]
    pub url: String,
    #[serde(default)]
    pub raw: String,
    #[serde(default)]
    pub internal: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Default)]
#[serde(rename_all = "camelCase")]
pub struct ProviderOption {
    #[serde(default)]
    pub value: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub required: bool,
    #[serde(default)]
    pub default: String,
    #[serde(default, rename = "type")]
    pub option_type: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Default)]
#[serde(rename_all = "camelCase")]
pub struct ProviderOptionGroup {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub options: Vec<String>,
    #[serde(default)]
    pub default_visible: bool,
}

/// Mirrors the JSON output from `devpod machine list --output json`.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Machine {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub provider: MachineProviderConfig,
    #[serde(default)]
    pub creation_timestamp: String,
    #[serde(default)]
    pub context: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Default)]
#[serde(rename_all = "camelCase")]
pub struct MachineProviderConfig {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub options: HashMap<String, OptionValue>,
}

/// Represents a devpod context.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Context {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub options: HashMap<String, String>,
}
```

- [ ] **Step 3: Register types module**

Update `daemon/mod.rs`:

```rust
pub mod cli;
pub mod types;
```

- [ ] **Step 4: Verify it compiles**

```bash
cd desktop-new/src-tauri
cargo check
```

Expected: Compiles without errors.

- [ ] **Step 5: Commit**

```bash
git add desktop-new/src-tauri/src/daemon/
git commit -m "feat(daemon): define workspace, provider, and machine types"
```

---

### Task 2: Build the DaemonState struct with diff detection

**Files:**
- Create: `desktop-new/src-tauri/src/daemon/state.rs`
- Modify: `desktop-new/src-tauri/src/daemon/mod.rs`

- [ ] **Step 1: Create daemon/state.rs with DaemonState**

```rust
use std::collections::HashMap;
use crate::daemon::types::{Context, Machine, Provider, Workspace};

/// The central in-memory state managed by the daemon.
#[derive(Debug, Clone, Default)]
pub struct DaemonState {
    pub workspaces: HashMap<String, Workspace>,
    pub providers: HashMap<String, Provider>,
    pub machines: HashMap<String, Machine>,
    pub contexts: Vec<Context>,
    pub active_context: String,
}

/// Describes what changed between two snapshots of state.
#[derive(Debug, Clone, Default)]
pub struct StateDiff {
    pub workspaces_changed: bool,
    pub providers_changed: bool,
    pub machines_changed: bool,
    pub contexts_changed: bool,
}

impl StateDiff {
    pub fn has_changes(&self) -> bool {
        self.workspaces_changed
            || self.providers_changed
            || self.machines_changed
            || self.contexts_changed
    }
}

impl DaemonState {
    pub fn new() -> Self {
        Self::default()
    }

    /// Replace workspaces from a fresh CLI fetch. Returns true if anything changed.
    pub fn update_workspaces(&mut self, new: Vec<Workspace>) -> bool {
        let new_map: HashMap<String, Workspace> = new
            .into_iter()
            .map(|w| (w.id.clone(), w))
            .collect();
        if self.workspaces == new_map {
            return false;
        }
        self.workspaces = new_map;
        true
    }

    /// Replace providers from a fresh CLI fetch. Returns true if anything changed.
    pub fn update_providers(&mut self, new: Vec<Provider>) -> bool {
        let new_map: HashMap<String, Provider> = new
            .into_iter()
            .map(|p| (p.name.clone(), p))
            .collect();
        if self.providers == new_map {
            return false;
        }
        self.providers = new_map;
        true
    }

    /// Replace machines from a fresh CLI fetch. Returns true if anything changed.
    pub fn update_machines(&mut self, new: Vec<Machine>) -> bool {
        let new_map: HashMap<String, Machine> = new
            .into_iter()
            .map(|m| (m.id.clone(), m))
            .collect();
        if self.machines == new_map {
            return false;
        }
        self.machines = new_map;
        true
    }

    /// Replace contexts. Returns true if anything changed.
    pub fn update_contexts(&mut self, new: Vec<Context>, active: String) -> bool {
        if self.contexts == new && self.active_context == active {
            return false;
        }
        self.contexts = new;
        self.active_context = active;
        true
    }

    /// Get sorted list of workspaces (by last used, descending).
    pub fn workspace_list(&self) -> Vec<Workspace> {
        let mut list: Vec<Workspace> = self.workspaces.values().cloned().collect();
        list.sort_by(|a, b| b.last_used_timestamp.cmp(&a.last_used_timestamp));
        list
    }

    /// Get sorted list of providers (by name).
    pub fn provider_list(&self) -> Vec<Provider> {
        let mut list: Vec<Provider> = self.providers.values().cloned().collect();
        list.sort_by(|a, b| a.name.cmp(&b.name));
        list
    }

    /// Get sorted list of machines (by ID).
    pub fn machine_list(&self) -> Vec<Machine> {
        let mut list: Vec<Machine> = self.machines.values().cloned().collect();
        list.sort_by(|a, b| a.id.cmp(&b.id));
        list
    }
}
```

- [ ] **Step 2: Register state module**

Update `daemon/mod.rs`:

```rust
pub mod cli;
pub mod state;
pub mod types;
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
git commit -m "feat(daemon): add DaemonState with update and diff detection"
```

---

### Task 3: Register DaemonState in Tauri app

**Files:**
- Modify: `desktop-new/src-tauri/src/main.rs`

- [ ] **Step 1: Create shared state and register with Tauri**

Update `main.rs`:

```rust
#![cfg_attr(
    all(not(debug_assertions), target_os = "windows"),
    windows_subsystem = "windows"
)]

mod daemon;

use daemon::state::DaemonState;
use std::sync::Arc;
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
        .manage(state)
        .setup(|app| {
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
git commit -m "feat(daemon): register shared DaemonState in Tauri app"
```

---

### Task 4: Write unit tests for state diffing

**Files:**
- Create: `desktop-new/src-tauri/src/daemon/state_test.rs`
- Modify: `desktop-new/src-tauri/src/daemon/state.rs`

- [ ] **Step 1: Add tests module to state.rs**

Append to the bottom of `daemon/state.rs`:

```rust
#[cfg(test)]
mod tests {
    use super::*;
    use crate::daemon::types::*;
    use std::collections::HashMap;

    fn make_workspace(id: &str) -> Workspace {
        Workspace {
            id: id.to_string(),
            uid: format!("uid-{}", id),
            picture: String::new(),
            provider: WorkspaceProviderConfig {
                name: "docker".to_string(),
                options: HashMap::new(),
            },
            machine: WorkspaceMachineConfig::default(),
            ide: WorkspaceIDEConfig {
                name: "vscode".to_string(),
                options: HashMap::new(),
            },
            source: WorkspaceSource {
                git_repository: "https://github.com/test/repo".to_string(),
                ..Default::default()
            },
            dev_container_image: String::new(),
            dev_container_path: String::new(),
            creation_timestamp: "2026-01-01T00:00:00Z".to_string(),
            last_used_timestamp: "2026-01-01T00:00:00Z".to_string(),
            context: "default".to_string(),
            imported: false,
            ssh_config_path: String::new(),
        }
    }

    fn make_provider(name: &str) -> Provider {
        Provider {
            name: name.to_string(),
            version: "1.0.0".to_string(),
            icon: String::new(),
            description: format!("{} provider", name),
            source: ProviderSource::default(),
            options: HashMap::new(),
            option_groups: vec![],
            is_default: false,
            state: String::new(),
        }
    }

    #[test]
    fn test_update_workspaces_detects_change() {
        let mut state = DaemonState::new();
        assert!(state.workspaces.is_empty());

        let changed = state.update_workspaces(vec![make_workspace("ws-1")]);
        assert!(changed);
        assert_eq!(state.workspaces.len(), 1);

        // Same data again — no change
        let changed = state.update_workspaces(vec![make_workspace("ws-1")]);
        assert!(!changed);
    }

    #[test]
    fn test_update_workspaces_detects_removal() {
        let mut state = DaemonState::new();
        state.update_workspaces(vec![make_workspace("ws-1"), make_workspace("ws-2")]);
        assert_eq!(state.workspaces.len(), 2);

        let changed = state.update_workspaces(vec![make_workspace("ws-1")]);
        assert!(changed);
        assert_eq!(state.workspaces.len(), 1);
    }

    #[test]
    fn test_update_providers_detects_change() {
        let mut state = DaemonState::new();
        let changed = state.update_providers(vec![make_provider("docker")]);
        assert!(changed);

        let changed = state.update_providers(vec![make_provider("docker")]);
        assert!(!changed);
    }

    #[test]
    fn test_workspace_list_sorted_by_last_used() {
        let mut state = DaemonState::new();
        let mut ws1 = make_workspace("ws-1");
        ws1.last_used_timestamp = "2026-01-01T00:00:00Z".to_string();
        let mut ws2 = make_workspace("ws-2");
        ws2.last_used_timestamp = "2026-01-02T00:00:00Z".to_string();

        state.update_workspaces(vec![ws1, ws2]);
        let list = state.workspace_list();
        assert_eq!(list[0].id, "ws-2"); // More recent first
        assert_eq!(list[1].id, "ws-1");
    }
}
```

- [ ] **Step 2: Run the tests**

```bash
cd desktop-new/src-tauri
cargo test daemon::state::tests
```

Expected: All 4 tests pass.

- [ ] **Step 3: Commit**

```bash
git add desktop-new/src-tauri/src/daemon/state.rs
git commit -m "test(daemon): add unit tests for DaemonState diffing"
```
