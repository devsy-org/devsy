# Plan 10: Terminal & System Tray

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a PTY manager in Rust for embedded terminal sessions, integrate xterm.js in the frontend for interactive terminals, create a terminal session manager view, and add a minimal system tray.

**Architecture:** The Rust `PtyManager` creates pseudo-terminal sessions via `portable-pty`. Each session has a UUID. The frontend sends input via Tauri commands and receives output via events. xterm.js renders the terminal. The system tray is minimal: show/hide window + quit.

**Tech Stack:** Rust (portable-pty, tokio), xterm.js, SvelteKit, Tauri tray API

---

### Task 1: Add PTY dependencies

**Files:**
- Modify: `desktop-new/src-tauri/Cargo.toml`

- [ ] **Step 1: Add portable-pty to Cargo.toml**

Add to `[dependencies]`:

```toml
portable-pty = "0.8"
uuid = { version = "1.0", features = ["v4"] }
```

- [ ] **Step 2: Verify it compiles**

```bash
cd desktop-new/src-tauri
cargo check
```

Expected: Compiles without errors.

- [ ] **Step 3: Commit**

```bash
git add desktop-new/src-tauri/Cargo.toml
git commit -m "feat(terminal): add portable-pty dependency"
```

---

### Task 2: Implement PTY manager

**Files:**
- Create: `desktop-new/src-tauri/src/terminal/mod.rs`
- Create: `desktop-new/src-tauri/src/terminal/pty.rs`
- Modify: `desktop-new/src-tauri/src/main.rs`

- [ ] **Step 1: Create terminal/mod.rs**

```rust
pub mod pty;
```

- [ ] **Step 2: Create terminal/pty.rs**

```rust
use log::{debug, error, info};
use portable_pty::{native_pty_system, CommandBuilder, MasterPty, PtySize};
use std::collections::HashMap;
use std::io::{Read, Write};
use std::sync::{Arc, Mutex};
use tauri::{AppHandle, Emitter, Wry};
use uuid::Uuid;

const TERMINAL_OUTPUT_EVENT: &str = "terminal:output";
const TERMINAL_EXIT_EVENT: &str = "terminal:exit";

#[derive(Debug, Clone, serde::Serialize)]
pub struct TerminalOutputPayload {
    pub session_id: String,
    pub data: Vec<u8>,
}

#[derive(Debug, Clone, serde::Serialize)]
pub struct TerminalExitPayload {
    pub session_id: String,
}

struct PtySession {
    master: Box<dyn MasterPty + Send>,
    writer: Box<dyn Write + Send>,
    _reader_handle: std::thread::JoinHandle<()>,
}

pub struct PtyManager {
    sessions: Arc<Mutex<HashMap<String, PtySession>>>,
    app_handle: AppHandle<Wry>,
}

impl PtyManager {
    pub fn new(app_handle: AppHandle<Wry>) -> Self {
        Self {
            sessions: Arc::new(Mutex::new(HashMap::new())),
            app_handle,
        }
    }

    /// Create a new terminal session running a shell.
    /// Returns the session ID.
    pub fn create_session(&self, cols: u16, rows: u16) -> Result<String, String> {
        let session_id = Uuid::new_v4().to_string();
        let pty_system = native_pty_system();

        let pair = pty_system
            .openpty(PtySize {
                rows,
                cols,
                pixel_width: 0,
                pixel_height: 0,
            })
            .map_err(|e| format!("Failed to open PTY: {}", e))?;

        // Determine shell
        let shell = if cfg!(windows) {
            "powershell.exe".to_string()
        } else {
            std::env::var("SHELL").unwrap_or_else(|_| "/bin/sh".to_string())
        };

        let mut cmd = CommandBuilder::new(&shell);
        cmd.cwd(dirs::home_dir().unwrap_or_else(|| std::path::PathBuf::from("/")));

        let _child = pair
            .slave
            .spawn_command(cmd)
            .map_err(|e| format!("Failed to spawn shell: {}", e))?;

        // Drop slave — we only need master
        drop(pair.slave);

        let mut reader = pair
            .master
            .try_clone_reader()
            .map_err(|e| format!("Failed to clone PTY reader: {}", e))?;

        let writer = pair
            .master
            .take_writer()
            .map_err(|e| format!("Failed to take PTY writer: {}", e))?;

        // Spawn reader thread that sends output to frontend
        let app = self.app_handle.clone();
        let sid = session_id.clone();
        let sessions_ref = self.sessions.clone();
        let reader_handle = std::thread::spawn(move || {
            let mut buf = [0u8; 4096];
            loop {
                match reader.read(&mut buf) {
                    Ok(0) => {
                        // PTY closed
                        info!("PTY session {} closed", sid);
                        let _ = app.emit(
                            TERMINAL_EXIT_EVENT,
                            TerminalExitPayload {
                                session_id: sid.clone(),
                            },
                        );
                        // Clean up session
                        if let Ok(mut sessions) = sessions_ref.lock() {
                            sessions.remove(&sid);
                        }
                        break;
                    }
                    Ok(n) => {
                        let _ = app.emit(
                            TERMINAL_OUTPUT_EVENT,
                            TerminalOutputPayload {
                                session_id: sid.clone(),
                                data: buf[..n].to_vec(),
                            },
                        );
                    }
                    Err(e) => {
                        error!("PTY read error for session {}: {}", sid, e);
                        break;
                    }
                }
            }
        });

        let session = PtySession {
            master: pair.master,
            writer,
            _reader_handle: reader_handle,
        };

        self.sessions
            .lock()
            .map_err(|_| "Lock poisoned".to_string())?
            .insert(session_id.clone(), session);

        info!("Created PTY session: {}", session_id);
        Ok(session_id)
    }

    /// Create a terminal session that SSHs into a workspace.
    pub fn create_ssh_session(
        &self,
        workspace_id: &str,
        cols: u16,
        rows: u16,
    ) -> Result<String, String> {
        let session_id = Uuid::new_v4().to_string();
        let pty_system = native_pty_system();

        let pair = pty_system
            .openpty(PtySize {
                rows,
                cols,
                pixel_width: 0,
                pixel_height: 0,
            })
            .map_err(|e| format!("Failed to open PTY: {}", e))?;

        let mut cmd = CommandBuilder::new("devpod");
        cmd.arg("ssh");
        cmd.arg(workspace_id);

        let _child = pair
            .slave
            .spawn_command(cmd)
            .map_err(|e| format!("Failed to spawn devpod ssh: {}", e))?;

        drop(pair.slave);

        let mut reader = pair
            .master
            .try_clone_reader()
            .map_err(|e| format!("Failed to clone PTY reader: {}", e))?;

        let writer = pair
            .master
            .take_writer()
            .map_err(|e| format!("Failed to take PTY writer: {}", e))?;

        let app = self.app_handle.clone();
        let sid = session_id.clone();
        let sessions_ref = self.sessions.clone();
        let reader_handle = std::thread::spawn(move || {
            let mut buf = [0u8; 4096];
            loop {
                match reader.read(&mut buf) {
                    Ok(0) => {
                        let _ = app.emit(
                            TERMINAL_EXIT_EVENT,
                            TerminalExitPayload {
                                session_id: sid.clone(),
                            },
                        );
                        if let Ok(mut sessions) = sessions_ref.lock() {
                            sessions.remove(&sid);
                        }
                        break;
                    }
                    Ok(n) => {
                        let _ = app.emit(
                            TERMINAL_OUTPUT_EVENT,
                            TerminalOutputPayload {
                                session_id: sid.clone(),
                                data: buf[..n].to_vec(),
                            },
                        );
                    }
                    Err(_) => break,
                }
            }
        });

        let session = PtySession {
            master: pair.master,
            writer,
            _reader_handle: reader_handle,
        };

        self.sessions
            .lock()
            .map_err(|_| "Lock poisoned".to_string())?
            .insert(session_id.clone(), session);

        info!("Created SSH PTY session for workspace {}: {}", workspace_id, session_id);
        Ok(session_id)
    }

    /// Write data (user input) to a terminal session.
    pub fn write_to_session(&self, session_id: &str, data: &[u8]) -> Result<(), String> {
        let mut sessions = self.sessions.lock().map_err(|_| "Lock poisoned".to_string())?;
        let session = sessions
            .get_mut(session_id)
            .ok_or_else(|| format!("Session {} not found", session_id))?;

        session
            .writer
            .write_all(data)
            .map_err(|e| format!("Write error: {}", e))?;
        session
            .writer
            .flush()
            .map_err(|e| format!("Flush error: {}", e))?;
        Ok(())
    }

    /// Resize a terminal session.
    pub fn resize_session(
        &self,
        session_id: &str,
        cols: u16,
        rows: u16,
    ) -> Result<(), String> {
        let sessions = self.sessions.lock().map_err(|_| "Lock poisoned".to_string())?;
        let session = sessions
            .get(session_id)
            .ok_or_else(|| format!("Session {} not found", session_id))?;

        session
            .master
            .resize(PtySize {
                rows,
                cols,
                pixel_width: 0,
                pixel_height: 0,
            })
            .map_err(|e| format!("Resize error: {}", e))?;
        Ok(())
    }

    /// Close a terminal session.
    pub fn close_session(&self, session_id: &str) -> Result<(), String> {
        let mut sessions = self.sessions.lock().map_err(|_| "Lock poisoned".to_string())?;
        sessions.remove(session_id);
        info!("Closed PTY session: {}", session_id);
        Ok(())
    }

    /// List active session IDs.
    pub fn list_sessions(&self) -> Result<Vec<String>, String> {
        let sessions = self.sessions.lock().map_err(|_| "Lock poisoned".to_string())?;
        Ok(sessions.keys().cloned().collect())
    }
}
```

- [ ] **Step 3: Register terminal module in main.rs**

Add `mod terminal;` and manage `PtyManager`:

```rust
mod terminal;
```

In setup, after managing other state:

```rust
            let pty_manager = Arc::new(terminal::pty::PtyManager::new(app_handle.clone()));
            app.manage(pty_manager);
```

- [ ] **Step 4: Verify it compiles**

```bash
cd desktop-new/src-tauri
cargo check
```

Expected: Compiles without errors.

- [ ] **Step 5: Commit**

```bash
git add desktop-new/src-tauri/
git commit -m "feat(terminal): implement PTY manager with session lifecycle"
```

---

### Task 3: Create Tauri commands for terminal

**Files:**
- Create: `desktop-new/src-tauri/src/commands/terminal.rs`
- Modify: `desktop-new/src-tauri/src/commands/mod.rs`
- Modify: `desktop-new/src-tauri/src/main.rs`

- [ ] **Step 1: Create commands/terminal.rs**

```rust
use crate::terminal::pty::PtyManager;
use std::sync::Arc;
use tauri::State;

#[tauri::command]
pub async fn terminal_create(
    pty: State<'_, Arc<PtyManager>>,
    cols: u16,
    rows: u16,
) -> Result<String, String> {
    pty.create_session(cols, rows)
}

#[tauri::command]
pub async fn terminal_create_ssh(
    pty: State<'_, Arc<PtyManager>>,
    workspace_id: String,
    cols: u16,
    rows: u16,
) -> Result<String, String> {
    pty.create_ssh_session(&workspace_id, cols, rows)
}

#[tauri::command]
pub async fn terminal_write(
    pty: State<'_, Arc<PtyManager>>,
    session_id: String,
    data: Vec<u8>,
) -> Result<(), String> {
    pty.write_to_session(&session_id, &data)
}

#[tauri::command]
pub async fn terminal_resize(
    pty: State<'_, Arc<PtyManager>>,
    session_id: String,
    cols: u16,
    rows: u16,
) -> Result<(), String> {
    pty.resize_session(&session_id, cols, rows)
}

#[tauri::command]
pub async fn terminal_close(
    pty: State<'_, Arc<PtyManager>>,
    session_id: String,
) -> Result<(), String> {
    pty.close_session(&session_id)
}

#[tauri::command]
pub async fn terminal_list(
    pty: State<'_, Arc<PtyManager>>,
) -> Result<Vec<String>, String> {
    pty.list_sessions()
}
```

- [ ] **Step 2: Register in commands/mod.rs**

```rust
pub mod machines;
pub mod providers;
pub mod terminal;
pub mod workspaces;
```

- [ ] **Step 3: Add terminal commands to invoke_handler in main.rs**

Add to the `generate_handler!` macro:

```rust
            // Terminal
            commands::terminal::terminal_create,
            commands::terminal::terminal_create_ssh,
            commands::terminal::terminal_write,
            commands::terminal::terminal_resize,
            commands::terminal::terminal_close,
            commands::terminal::terminal_list,
```

- [ ] **Step 4: Verify it compiles**

```bash
cd desktop-new/src-tauri
cargo check
```

- [ ] **Step 5: Commit**

```bash
git add desktop-new/src-tauri/
git commit -m "feat(terminal): add Tauri commands for terminal sessions"
```

---

### Task 4: Add xterm.js and terminal TypeScript layer

**Files:**
- Modify: `desktop-new/package.json`
- Create: `desktop-new/src/lib/ipc/terminal.ts`
- Create: `desktop-new/src/lib/stores/terminals.ts`

- [ ] **Step 1: Install xterm.js**

```bash
cd desktop-new
npm install @xterm/xterm @xterm/addon-fit
```

- [ ] **Step 2: Create lib/ipc/terminal.ts**

```ts
import { invoke } from "@tauri-apps/api/core";
import { listen, type UnlistenFn } from "@tauri-apps/api/event";

export async function terminalCreate(cols: number, rows: number): Promise<string> {
  return invoke("terminal_create", { cols, rows });
}

export async function terminalCreateSsh(
  workspaceId: string,
  cols: number,
  rows: number,
): Promise<string> {
  return invoke("terminal_create_ssh", { workspaceId, cols, rows });
}

export async function terminalWrite(
  sessionId: string,
  data: Uint8Array,
): Promise<void> {
  return invoke("terminal_write", { sessionId, data: Array.from(data) });
}

export async function terminalResize(
  sessionId: string,
  cols: number,
  rows: number,
): Promise<void> {
  return invoke("terminal_resize", { sessionId, cols, rows });
}

export async function terminalClose(sessionId: string): Promise<void> {
  return invoke("terminal_close", { sessionId });
}

export async function terminalListSessions(): Promise<string[]> {
  return invoke("terminal_list");
}

interface TerminalOutputPayload {
  session_id: string;
  data: number[];
}

interface TerminalExitPayload {
  session_id: string;
}

export function onTerminalOutput(
  callback: (sessionId: string, data: Uint8Array) => void,
): Promise<UnlistenFn> {
  return listen<TerminalOutputPayload>("terminal:output", (event) => {
    callback(event.payload.session_id, new Uint8Array(event.payload.data));
  });
}

export function onTerminalExit(
  callback: (sessionId: string) => void,
): Promise<UnlistenFn> {
  return listen<TerminalExitPayload>("terminal:exit", (event) => {
    callback(event.payload.session_id);
  });
}
```

- [ ] **Step 3: Create lib/stores/terminals.ts**

```ts
import { writable, derived } from "svelte/store";

export interface TerminalSession {
  id: string;
  label: string;
  type: "shell" | "ssh";
  workspaceId?: string;
}

function createTerminalsStore() {
  const { subscribe, update, set } = writable<TerminalSession[]>([]);

  return {
    subscribe,
    add(session: TerminalSession) {
      update((sessions) => [...sessions, session]);
    },
    remove(sessionId: string) {
      update((sessions) => sessions.filter((s) => s.id !== sessionId));
    },
    clear() {
      set([]);
    },
  };
}

export const terminals = createTerminalsStore();

export const terminalCount = derived(terminals, ($t) => $t.length);
```

- [ ] **Step 4: Verify build**

```bash
cd desktop-new
npm run build
```

- [ ] **Step 5: Commit**

```bash
git add desktop-new/
git commit -m "feat(terminal): add xterm.js, terminal IPC layer, and session store"
```

---

### Task 5: Create xterm.js Svelte wrapper component

**Files:**
- Create: `desktop-new/src/lib/components/terminal/Terminal.svelte`

- [ ] **Step 1: Create Terminal.svelte**

```svelte
<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import {
    terminalWrite,
    terminalResize,
    onTerminalOutput,
    onTerminalExit,
  } from "$lib/ipc/terminal";
  import "@xterm/xterm/css/xterm.css";

  interface Props {
    sessionId: string;
    onExit?: () => void;
  }

  let { sessionId, onExit }: Props = $props();

  let containerEl: HTMLDivElement;
  let term: Terminal;
  let fitAddon: FitAddon;
  let unlistenOutput: (() => void) | null = null;
  let unlistenExit: (() => void) | null = null;
  let resizeObserver: ResizeObserver | null = null;

  onMount(async () => {
    term = new Terminal({
      cursorBlink: true,
      fontSize: 14,
      fontFamily: "monospace",
      theme: {
        background: "#1a1a2e",
        foreground: "#e0e0e0",
      },
    });

    fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.open(containerEl);
    fitAddon.fit();

    // Send user input to PTY
    term.onData((data) => {
      const encoder = new TextEncoder();
      terminalWrite(sessionId, encoder.encode(data));
    });

    // Receive output from PTY
    unlistenOutput = await onTerminalOutput((sid, data) => {
      if (sid === sessionId) {
        term.write(data);
      }
    });

    // Handle session exit
    unlistenExit = await onTerminalExit((sid) => {
      if (sid === sessionId) {
        term.write("\r\n[Session ended]\r\n");
        onExit?.();
      }
    });

    // Auto-resize
    resizeObserver = new ResizeObserver(() => {
      fitAddon.fit();
      terminalResize(sessionId, term.cols, term.rows);
    });
    resizeObserver.observe(containerEl);
  });

  onDestroy(() => {
    if (unlistenOutput) unlistenOutput();
    if (unlistenExit) unlistenExit();
    if (resizeObserver) resizeObserver.disconnect();
    if (term) term.dispose();
  });
</script>

<div bind:this={containerEl} class="h-full w-full"></div>
```

- [ ] **Step 2: Verify build**

```bash
cd desktop-new
npm run build
```

- [ ] **Step 3: Commit**

```bash
git add desktop-new/src/lib/components/terminal/
git commit -m "feat(terminal): create xterm.js Svelte wrapper component"
```

---

### Task 6: Build terminal manager view

**Files:**
- Modify: `desktop-new/src/routes/terminals/+page.svelte`

- [ ] **Step 1: Replace stub with terminal manager**

```svelte
<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import Terminal from "$lib/components/terminal/Terminal.svelte";
  import { terminals, type TerminalSession } from "$lib/stores/terminals";
  import { terminalCreate, terminalClose } from "$lib/ipc/terminal";

  let activeSessionId = $state<string | null>(null);

  let activeSession = $derived(
    $terminals.find((s) => s.id === activeSessionId)
  );

  async function createShell() {
    try {
      const sessionId = await terminalCreate(120, 30);
      const session: TerminalSession = {
        id: sessionId,
        label: `Shell ${$terminals.length + 1}`,
        type: "shell",
      };
      terminals.add(session);
      activeSessionId = sessionId;
    } catch (e) {
      console.error("Failed to create terminal:", e);
    }
  }

  async function closeSession(sessionId: string) {
    try {
      await terminalClose(sessionId);
    } catch {}
    terminals.remove(sessionId);
    if (activeSessionId === sessionId) {
      activeSessionId = $terminals[0]?.id ?? null;
    }
  }

  function handleExit() {
    if (activeSessionId) {
      terminals.remove(activeSessionId);
      activeSessionId = $terminals[0]?.id ?? null;
    }
  }
</script>

<div class="flex flex-col h-full">
  <div class="flex items-center justify-between mb-4">
    <h2 class="text-2xl font-bold">Terminals</h2>
    <Button onclick={createShell}>New Shell</Button>
  </div>

  {#if $terminals.length === 0}
    <div class="flex flex-col items-center justify-center flex-1 text-muted-foreground">
      <p>No active terminals.</p>
      <Button variant="outline" class="mt-4" onclick={createShell}>Open a terminal</Button>
    </div>
  {:else}
    <!-- Tab bar -->
    <div class="flex gap-1 border-b border-border mb-2 overflow-x-auto">
      {#each $terminals as session (session.id)}
        <button
          class="flex items-center gap-2 px-3 py-1.5 text-sm rounded-t transition-colors
            {activeSessionId === session.id
            ? 'bg-background border border-b-0 border-border text-foreground'
            : 'text-muted-foreground hover:text-foreground'}"
          onclick={() => (activeSessionId = session.id)}
        >
          <span>{session.label}</span>
          <button
            class="text-muted-foreground hover:text-destructive text-xs"
            onclick={(e) => { e.stopPropagation(); closeSession(session.id); }}
          >
            x
          </button>
        </button>
      {/each}
    </div>

    <!-- Terminal area -->
    <div class="flex-1 min-h-0 bg-muted rounded">
      {#if activeSession}
        {#key activeSession.id}
          <Terminal sessionId={activeSession.id} onExit={handleExit} />
        {/key}
      {/if}
    </div>
  {/if}
</div>
```

- [ ] **Step 2: Update sidebar to show terminal badge**

Update `src/routes/+layout.svelte` to pass terminal count:

Add import:
```ts
import { terminalCount } from "$lib/stores/terminals";
```

Update Sidebar usage:
```svelte
<Sidebar terminalCount={$terminalCount} />
```

- [ ] **Step 3: Verify build**

```bash
cd desktop-new
npm run build
```

- [ ] **Step 4: Commit**

```bash
git add desktop-new/src/
git commit -m "feat(terminal): build terminal manager view with tabs"
```

---

### Task 7: Add minimal system tray

**Files:**
- Create: `desktop-new/src-tauri/src/tray.rs`
- Modify: `desktop-new/src-tauri/src/main.rs`

- [ ] **Step 1: Create tray.rs**

```rust
use tauri::{
    image::Image,
    menu::{MenuBuilder, MenuItemBuilder},
    tray::TrayIconBuilder,
    AppHandle, Manager, Wry,
};

pub fn setup_tray(app: &AppHandle<Wry>) -> Result<(), Box<dyn std::error::Error>> {
    let show = MenuItemBuilder::with_id("show", "Show DevPod").build(app)?;
    let hide = MenuItemBuilder::with_id("hide", "Hide").build(app)?;
    let quit = MenuItemBuilder::with_id("quit", "Quit").build(app)?;

    let menu = MenuBuilder::new(app)
        .item(&show)
        .item(&hide)
        .separator()
        .item(&quit)
        .build()?;

    let _tray = TrayIconBuilder::new()
        .menu(&menu)
        .tooltip("DevPod")
        .on_menu_event(move |app, event| match event.id().as_ref() {
            "show" => {
                if let Some(window) = app.get_webview_window("main") {
                    let _ = window.show();
                    let _ = window.set_focus();
                }
            }
            "hide" => {
                if let Some(window) = app.get_webview_window("main") {
                    let _ = window.hide();
                }
            }
            "quit" => {
                app.exit(0);
            }
            _ => {}
        })
        .build(app)?;

    Ok(())
}
```

- [ ] **Step 2: Wire tray into main.rs setup**

Add `mod tray;` and call `tray::setup_tray` in setup:

```rust
mod tray;
```

In setup, after showing window:

```rust
            if let Err(e) = tray::setup_tray(&app_handle) {
                log::error!("Failed to setup system tray: {}", e);
            }
```

- [ ] **Step 3: Verify it compiles**

```bash
cd desktop-new/src-tauri
cargo check
```

- [ ] **Step 4: Commit**

```bash
git add desktop-new/src-tauri/
git commit -m "feat(tray): add minimal system tray with show/hide/quit"
```

---

### Task 8: Build settings view

**Files:**
- Modify: `desktop-new/src/routes/settings/+page.svelte`

- [ ] **Step 1: Replace stub with settings page**

```svelte
<script lang="ts">
  import { Card } from "$lib/components/ui/card";
  import { Label } from "$lib/components/ui/label";
  import { Separator } from "$lib/components/ui/separator";
  import { Button } from "$lib/components/ui/button";
  import { settings, type Theme } from "$lib/stores/settings";

  let currentTheme = $state<Theme>("system");

  settings.subscribe((s) => {
    currentTheme = s.theme;
  });

  const themes: { value: Theme; label: string; description: string }[] = [
    { value: "light", label: "Light", description: "Light theme" },
    { value: "dark", label: "Dark", description: "Dark theme" },
    { value: "system", label: "System", description: "Follow system preference" },
  ];
</script>

<h2 class="text-2xl font-bold mb-6">Settings</h2>

<div class="max-w-2xl flex flex-col gap-6">
  <Card class="p-6">
    <h3 class="text-lg font-semibold mb-4">Appearance</h3>
    <div class="flex flex-col gap-2">
      <Label>Theme</Label>
      <div class="flex gap-2">
        {#each themes as theme (theme.value)}
          <Button
            variant={currentTheme === theme.value ? "default" : "outline"}
            size="sm"
            onclick={() => settings.setTheme(theme.value)}
          >
            {theme.label}
          </Button>
        {/each}
      </div>
    </div>
  </Card>

  <Card class="p-6">
    <h3 class="text-lg font-semibold mb-4">About</h3>
    <dl class="grid grid-cols-2 gap-4 text-sm">
      <div>
        <dt class="text-muted-foreground">Application</dt>
        <dd class="mt-1">DevPod Desktop</dd>
      </div>
      <div>
        <dt class="text-muted-foreground">Version</dt>
        <dd class="mt-1">0.1.0</dd>
      </div>
    </dl>
  </Card>
</div>
```

- [ ] **Step 2: Verify build**

```bash
cd desktop-new
npm run build
```

- [ ] **Step 3: Commit**

```bash
git add desktop-new/src/routes/settings/
git commit -m "feat(ui): build settings view with theme selection"
```
