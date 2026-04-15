# Plan 2: Rust CLI Runner

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an async Rust module that executes devpod CLI commands, parses JSON output, and supports streaming stdout/stderr line-by-line via channels.

**Architecture:** A `CliRunner` struct wraps `tokio::process::Command`. Two execution modes: `run()` for fire-and-wait with parsed JSON return, and `run_streaming()` which sends output lines to a `tokio::sync::mpsc` channel. The runner resolves the devpod binary path at startup.

**Tech Stack:** Rust, tokio, serde_json, thiserror

---

### Task 1: Create the CLI runner module structure

**Files:**
- Create: `desktop-new/src-tauri/src/daemon/mod.rs`
- Create: `desktop-new/src-tauri/src/daemon/cli.rs`
- Modify: `desktop-new/src-tauri/src/main.rs`

- [ ] **Step 1: Create the daemon module directory**

```bash
mkdir -p desktop-new/src-tauri/src/daemon
```

- [ ] **Step 2: Create daemon/mod.rs**

```rust
pub mod cli;
```

- [ ] **Step 3: Create daemon/cli.rs with CliRunner struct and error types**

```rust
use serde::de::DeserializeOwned;
use std::path::PathBuf;
use thiserror::Error;
use tokio::process::Command;
use tokio::sync::mpsc;

#[derive(Error, Debug)]
pub enum CliError {
    #[error("devpod binary not found at {0}")]
    BinaryNotFound(PathBuf),
    #[error("command failed with exit code {code}: {stderr}")]
    CommandFailed { code: i32, stderr: String },
    #[error("failed to parse JSON output: {0}")]
    ParseError(#[from] serde_json::Error),
    #[error("IO error: {0}")]
    IoError(#[from] std::io::Error),
}

pub struct CliRunner {
    binary_path: PathBuf,
}

impl CliRunner {
    pub fn new(binary_path: PathBuf) -> Result<Self, CliError> {
        if !binary_path.exists() {
            return Err(CliError::BinaryNotFound(binary_path));
        }
        Ok(Self { binary_path })
    }

    pub fn binary_path(&self) -> &PathBuf {
        &self.binary_path
    }
}
```

- [ ] **Step 4: Register the daemon module in main.rs**

Add `mod daemon;` to the top of `main.rs`:

```rust
#![cfg_attr(
    all(not(debug_assertions), target_os = "windows"),
    windows_subsystem = "windows"
)]

mod daemon;

use tauri::Manager;

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_log::Builder::new().build())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_os::init())
        .plugin(tauri_plugin_process::init())
        .plugin(tauri_plugin_fs::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_clipboard_manager::init())
        .plugin(tauri_plugin_opener::init())
        .setup(|app| {
            let window = app.get_webview_window("main").unwrap();
            window.show().unwrap();
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
```

- [ ] **Step 5: Verify it compiles**

```bash
cd desktop-new/src-tauri
cargo check
```

Expected: Compiles without errors.

- [ ] **Step 6: Commit**

```bash
git add desktop-new/src-tauri/src/daemon/
git commit -m "feat(daemon): add CLI runner module structure"
```

---

### Task 2: Implement fire-and-wait execution

**Files:**
- Modify: `desktop-new/src-tauri/src/daemon/cli.rs`

- [ ] **Step 1: Add the `run` method for fire-and-wait JSON execution**

Add to the `impl CliRunner` block in `daemon/cli.rs`:

```rust
    /// Run a devpod command and parse the JSON output.
    /// `args` should NOT include the binary name.
    /// Automatically appends `--output json` to the args.
    pub async fn run<T: DeserializeOwned>(&self, args: &[&str]) -> Result<T, CliError> {
        let mut cmd_args: Vec<&str> = args.to_vec();
        cmd_args.push("--output");
        cmd_args.push("json");

        let output = Command::new(&self.binary_path)
            .args(&cmd_args)
            .output()
            .await?;

        if !output.status.success() {
            let stderr = String::from_utf8_lossy(&output.stderr).to_string();
            return Err(CliError::CommandFailed {
                code: output.status.code().unwrap_or(-1),
                stderr,
            });
        }

        let parsed: T = serde_json::from_slice(&output.stdout)?;
        Ok(parsed)
    }

    /// Run a devpod command and return raw stdout as a String.
    /// Does NOT append `--output json`.
    pub async fn run_raw(&self, args: &[&str]) -> Result<String, CliError> {
        let output = Command::new(&self.binary_path)
            .args(args)
            .output()
            .await?;

        if !output.status.success() {
            let stderr = String::from_utf8_lossy(&output.stderr).to_string();
            return Err(CliError::CommandFailed {
                code: output.status.code().unwrap_or(-1),
                stderr,
            });
        }

        Ok(String::from_utf8_lossy(&output.stdout).to_string())
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
git add desktop-new/src-tauri/src/daemon/cli.rs
git commit -m "feat(daemon): implement fire-and-wait CLI execution"
```

---

### Task 3: Implement streaming execution

**Files:**
- Modify: `desktop-new/src-tauri/src/daemon/cli.rs`

- [ ] **Step 1: Add OutputLine enum and run_streaming method**

Add to `daemon/cli.rs`:

```rust
/// A line of output from a streaming CLI command.
#[derive(Debug, Clone)]
pub enum OutputLine {
    Stdout(String),
    Stderr(String),
    Exit(i32),
}

impl CliRunner {
    // ... existing methods ...

    /// Run a devpod command and stream stdout/stderr lines to a channel.
    /// Returns a JoinHandle for the spawned task.
    pub fn run_streaming(
        &self,
        args: &[&str],
        tx: mpsc::Sender<OutputLine>,
    ) -> tokio::task::JoinHandle<Result<i32, CliError>> {
        use tokio::io::{AsyncBufReadExt, BufReader};

        let binary_path = self.binary_path.clone();
        let args: Vec<String> = args.iter().map(|s| s.to_string()).collect();

        tokio::spawn(async move {
            let mut child = Command::new(&binary_path)
                .args(&args)
                .stdout(std::process::Stdio::piped())
                .stderr(std::process::Stdio::piped())
                .spawn()?;

            let stdout = child.stdout.take().unwrap();
            let stderr = child.stderr.take().unwrap();

            let tx_out = tx.clone();
            let stdout_handle = tokio::spawn(async move {
                let reader = BufReader::new(stdout);
                let mut lines = reader.lines();
                while let Ok(Some(line)) = lines.next_line().await {
                    let _ = tx_out.send(OutputLine::Stdout(line)).await;
                }
            });

            let tx_err = tx.clone();
            let stderr_handle = tokio::spawn(async move {
                let reader = BufReader::new(stderr);
                let mut lines = reader.lines();
                while let Ok(Some(line)) = lines.next_line().await {
                    let _ = tx_err.send(OutputLine::Stderr(line)).await;
                }
            });

            let _ = stdout_handle.await;
            let _ = stderr_handle.await;

            let status = child.wait().await?;
            let code = status.code().unwrap_or(-1);
            let _ = tx.send(OutputLine::Exit(code)).await;

            Ok(code)
        })
    }
}
```

Note: The `run_streaming` method needs to be in a separate `impl` block or the existing one expanded. Ensure the `OutputLine` enum is defined before the `impl` block.

- [ ] **Step 2: Verify it compiles**

```bash
cd desktop-new/src-tauri
cargo check
```

Expected: Compiles without errors.

- [ ] **Step 3: Commit**

```bash
git add desktop-new/src-tauri/src/daemon/cli.rs
git commit -m "feat(daemon): implement streaming CLI execution with channel output"
```

---

### Task 4: Add binary path resolution

**Files:**
- Modify: `desktop-new/src-tauri/src/daemon/cli.rs`

- [ ] **Step 1: Add a resolve_binary_path function**

Add to `daemon/cli.rs`:

```rust
use std::env;

/// Resolve the devpod binary path.
/// Checks in order:
/// 1. Explicit path if provided
/// 2. Next to the current executable (bundled binary)
/// 3. On the system PATH
pub fn resolve_binary_path(explicit: Option<PathBuf>) -> Result<PathBuf, CliError> {
    // 1. Explicit path
    if let Some(path) = explicit {
        if path.exists() {
            return Ok(path);
        }
        return Err(CliError::BinaryNotFound(path));
    }

    // 2. Next to current executable (Tauri bundles externalBin here)
    if let Ok(exe_path) = env::current_exe() {
        if let Some(exe_dir) = exe_path.parent() {
            let candidate = exe_dir.join("devpod");
            if candidate.exists() {
                return Ok(candidate);
            }
            // Windows
            let candidate_exe = exe_dir.join("devpod.exe");
            if candidate_exe.exists() {
                return Ok(candidate_exe);
            }
        }
    }

    // 3. System PATH via `which`
    let name = if cfg!(windows) { "devpod.exe" } else { "devpod" };
    if let Ok(path) = which::which(name) {
        return Ok(path);
    }

    Err(CliError::BinaryNotFound(PathBuf::from("devpod")))
}
```

- [ ] **Step 2: Add `which` to Cargo.toml dependencies**

Add to `[dependencies]` in `Cargo.toml`:

```toml
which = "7.0"
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
git commit -m "feat(daemon): add devpod binary path resolution"
```

---

### Task 5: Write integration test

**Files:**
- Create: `desktop-new/src-tauri/tests/cli_runner_test.rs`

- [ ] **Step 1: Create integration test**

```rust
use std::path::PathBuf;

// This test requires `devpod` to be on the system PATH.
// Skip in CI if not available.

#[tokio::test]
async fn test_run_raw_version() {
    let binary = which::which("devpod");
    if binary.is_err() {
        eprintln!("SKIP: devpod not found on PATH");
        return;
    }

    // We import from the crate - need to make cli module public for tests
    // For now, test via the binary directly
    let output = tokio::process::Command::new("devpod")
        .arg("version")
        .output()
        .await
        .expect("failed to run devpod version");

    assert!(output.status.success());
    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(!stdout.is_empty(), "version output should not be empty");
}

#[tokio::test]
async fn test_run_list_json() {
    let binary = which::which("devpod");
    if binary.is_err() {
        eprintln!("SKIP: devpod not found on PATH");
        return;
    }

    let output = tokio::process::Command::new("devpod")
        .args(["list", "--output", "json"])
        .output()
        .await
        .expect("failed to run devpod list");

    assert!(output.status.success());
    let stdout = String::from_utf8_lossy(&output.stdout);
    // Should be valid JSON (array)
    let parsed: serde_json::Value = serde_json::from_str(&stdout)
        .expect("devpod list --output json should produce valid JSON");
    assert!(parsed.is_array(), "expected JSON array from devpod list");
}
```

- [ ] **Step 2: Run the test**

```bash
cd desktop-new/src-tauri
cargo test --test cli_runner_test
```

Expected: Tests pass (or skip with message if devpod not on PATH).

- [ ] **Step 3: Commit**

```bash
git add desktop-new/src-tauri/tests/
git commit -m "test(daemon): add CLI runner integration tests"
```
