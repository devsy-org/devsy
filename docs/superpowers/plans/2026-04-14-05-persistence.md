# Plan 5: Persistence

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add workspace log file storage for customer-facing log history and a SQLite audit log for development event auditing.

**Architecture:** Workspace logs are stored as individual files under `~/.devpod/logs/{workspace_id}/`. The SQLite audit log at `~/.devpod/audit.db` records every daemon event, CLI invocation, and state change. Both are managed by a `Persistence` struct initialized at app startup.

**Tech Stack:** Rust, rusqlite, tokio, chrono

---

### Task 1: Add persistence dependencies

**Files:**
- Modify: `desktop-new/src-tauri/Cargo.toml`

- [ ] **Step 1: Add rusqlite and chrono to Cargo.toml**

Add to `[dependencies]`:

```toml
rusqlite = { version = "0.32", features = ["bundled"] }
chrono = { version = "0.4", features = ["serde"] }
```

- [ ] **Step 2: Verify it compiles**

```bash
cd desktop-new/src-tauri
cargo check
```

Expected: Compiles (rusqlite bundled will compile sqlite from source — may take a minute on first build).

- [ ] **Step 3: Commit**

```bash
git add desktop-new/src-tauri/Cargo.toml
git commit -m "feat(persistence): add rusqlite and chrono dependencies"
```

---

### Task 2: Implement workspace log storage

**Files:**
- Create: `desktop-new/src-tauri/src/persistence/mod.rs`
- Create: `desktop-new/src-tauri/src/persistence/logs.rs`
- Modify: `desktop-new/src-tauri/src/main.rs`

- [ ] **Step 1: Create persistence/mod.rs**

```rust
pub mod logs;
pub mod audit;
```

- [ ] **Step 2: Create persistence/logs.rs**

```rust
use chrono::Utc;
use log::{error, info};
use std::fs;
use std::path::{Path, PathBuf};
use thiserror::Error;

#[derive(Error, Debug)]
pub enum LogError {
    #[error("IO error: {0}")]
    Io(#[from] std::io::Error),
}

/// Metadata about a stored log file.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct LogEntry {
    pub workspace_id: String,
    pub command: String,
    pub timestamp: String,
    pub file_path: String,
}

pub struct LogStore {
    base_dir: PathBuf,
    retention_days: u64,
}

impl LogStore {
    /// Create a new LogStore. Creates the base directory if needed.
    pub fn new(base_dir: PathBuf, retention_days: u64) -> Result<Self, LogError> {
        fs::create_dir_all(&base_dir)?;
        Ok(Self {
            base_dir,
            retention_days,
        })
    }

    /// Default log directory: ~/.devpod/logs/
    pub fn default_dir() -> PathBuf {
        dirs::home_dir()
            .unwrap_or_else(|| PathBuf::from("."))
            .join(".devpod")
            .join("logs")
    }

    /// Create a new log file for a workspace command.
    /// Returns the path to the log file.
    pub fn create_log_file(
        &self,
        workspace_id: &str,
        command: &str,
    ) -> Result<PathBuf, LogError> {
        let workspace_dir = self.base_dir.join(workspace_id);
        fs::create_dir_all(&workspace_dir)?;

        let timestamp = Utc::now().format("%Y%m%dT%H%M%SZ");
        let filename = format!("{}-{}.log", timestamp, command);
        let file_path = workspace_dir.join(&filename);

        // Create empty file
        fs::File::create(&file_path)?;
        Ok(file_path)
    }

    /// Append a line to an existing log file.
    pub fn append_log(path: &Path, line: &str) -> Result<(), LogError> {
        use std::io::Write;
        let mut file = fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(path)?;
        writeln!(file, "{}", line)?;
        Ok(())
    }

    /// List all log entries for a workspace, sorted newest first.
    pub fn list_logs(&self, workspace_id: &str) -> Result<Vec<LogEntry>, LogError> {
        let workspace_dir = self.base_dir.join(workspace_id);
        if !workspace_dir.exists() {
            return Ok(vec![]);
        }

        let mut entries = Vec::new();
        for entry in fs::read_dir(&workspace_dir)? {
            let entry = entry?;
            let path = entry.path();
            if path.extension().and_then(|e| e.to_str()) != Some("log") {
                continue;
            }
            if let Some(filename) = path.file_stem().and_then(|f| f.to_str()) {
                // Parse filename: "20260414T120000Z-up" -> timestamp + command
                if let Some((timestamp, command)) = filename.split_once('-') {
                    entries.push(LogEntry {
                        workspace_id: workspace_id.to_string(),
                        command: command.to_string(),
                        timestamp: timestamp.to_string(),
                        file_path: path.to_string_lossy().to_string(),
                    });
                }
            }
        }

        entries.sort_by(|a, b| b.timestamp.cmp(&a.timestamp));
        Ok(entries)
    }

    /// Read the full contents of a log file.
    pub fn read_log(path: &Path) -> Result<String, LogError> {
        Ok(fs::read_to_string(path)?)
    }

    /// Prune logs older than retention_days.
    pub fn prune(&self) -> Result<u64, LogError> {
        let cutoff = Utc::now() - chrono::Duration::days(self.retention_days as i64);
        let cutoff_str = cutoff.format("%Y%m%dT%H%M%SZ").to_string();
        let mut removed = 0u64;

        if !self.base_dir.exists() {
            return Ok(0);
        }

        for workspace_entry in fs::read_dir(&self.base_dir)? {
            let workspace_entry = workspace_entry?;
            if !workspace_entry.file_type()?.is_dir() {
                continue;
            }
            for log_entry in fs::read_dir(workspace_entry.path())? {
                let log_entry = log_entry?;
                let path = log_entry.path();
                if let Some(filename) = path.file_stem().and_then(|f| f.to_str()) {
                    if let Some((timestamp, _)) = filename.split_once('-') {
                        if timestamp < cutoff_str.as_str() {
                            fs::remove_file(&path)?;
                            removed += 1;
                        }
                    }
                }
            }
            // Remove empty workspace dirs
            if fs::read_dir(workspace_entry.path())?.next().is_none() {
                fs::remove_dir(workspace_entry.path())?;
            }
        }

        if removed > 0 {
            info!("Pruned {} old log files", removed);
        }
        Ok(removed)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;

    #[test]
    fn test_create_and_list_logs() {
        let tmp = tempfile::tempdir().unwrap();
        let store = LogStore::new(tmp.path().to_path_buf(), 30).unwrap();

        let path = store.create_log_file("ws-1", "up").unwrap();
        assert!(path.exists());

        LogStore::append_log(&path, "Starting workspace...").unwrap();
        LogStore::append_log(&path, "Done.").unwrap();

        let content = LogStore::read_log(&path).unwrap();
        assert!(content.contains("Starting workspace..."));
        assert!(content.contains("Done."));

        let entries = store.list_logs("ws-1").unwrap();
        assert_eq!(entries.len(), 1);
        assert_eq!(entries[0].command, "up");
    }

    #[test]
    fn test_list_logs_empty_workspace() {
        let tmp = tempfile::tempdir().unwrap();
        let store = LogStore::new(tmp.path().to_path_buf(), 30).unwrap();
        let entries = store.list_logs("nonexistent").unwrap();
        assert!(entries.is_empty());
    }
}
```

- [ ] **Step 3: Add tempfile dev dependency to Cargo.toml**

Add to `[dev-dependencies]`:

```toml
[dev-dependencies]
tempfile = "3.10"
```

- [ ] **Step 4: Register persistence module in main.rs**

Add `mod persistence;` after `mod events;`:

```rust
mod daemon;
mod events;
mod persistence;
```

- [ ] **Step 5: Verify it compiles and tests pass**

```bash
cd desktop-new/src-tauri
cargo test persistence::logs::tests
```

Expected: Both tests pass.

- [ ] **Step 6: Commit**

```bash
git add desktop-new/src-tauri/
git commit -m "feat(persistence): implement workspace log file storage"
```

---

### Task 3: Implement SQLite audit log

**Files:**
- Create: `desktop-new/src-tauri/src/persistence/audit.rs`

- [ ] **Step 1: Create persistence/audit.rs**

```rust
use chrono::Utc;
use rusqlite::{params, Connection};
use std::path::PathBuf;
use std::sync::Mutex;
use thiserror::Error;

#[derive(Error, Debug)]
pub enum AuditError {
    #[error("SQLite error: {0}")]
    Sqlite(#[from] rusqlite::Error),
    #[error("Lock poisoned")]
    LockPoisoned,
}

/// A row in the audit events table.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct AuditEvent {
    pub id: i64,
    pub timestamp: String,
    pub event_type: String,
    pub resource_id: Option<String>,
    pub command: Option<String>,
    pub payload: Option<String>,
    pub duration_ms: Option<i64>,
    pub status: String,
}

pub struct AuditLog {
    conn: Mutex<Connection>,
}

impl AuditLog {
    /// Open or create the audit database. Creates the events table if needed.
    pub fn open(path: PathBuf) -> Result<Self, AuditError> {
        // Ensure parent directory exists
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent).ok();
        }

        let conn = Connection::open(&path)?;

        // Enable WAL mode for concurrent reads during writes
        conn.execute_batch("PRAGMA journal_mode=WAL;")?;

        conn.execute(
            "CREATE TABLE IF NOT EXISTS events (
                id          INTEGER PRIMARY KEY AUTOINCREMENT,
                timestamp   TEXT NOT NULL,
                event_type  TEXT NOT NULL,
                resource_id TEXT,
                command     TEXT,
                payload     TEXT,
                duration_ms INTEGER,
                status      TEXT NOT NULL
            )",
            [],
        )?;

        // Index for common queries
        conn.execute(
            "CREATE INDEX IF NOT EXISTS idx_events_type_ts ON events (event_type, timestamp)",
            [],
        )?;
        conn.execute(
            "CREATE INDEX IF NOT EXISTS idx_events_resource ON events (resource_id, timestamp)",
            [],
        )?;

        Ok(Self {
            conn: Mutex::new(conn),
        })
    }

    /// Default path: ~/.devpod/audit.db
    pub fn default_path() -> PathBuf {
        dirs::home_dir()
            .unwrap_or_else(|| PathBuf::from("."))
            .join(".devpod")
            .join("audit.db")
    }

    /// Record an event.
    pub fn record(
        &self,
        event_type: &str,
        resource_id: Option<&str>,
        command: Option<&str>,
        payload: Option<&str>,
        duration_ms: Option<i64>,
        status: &str,
    ) -> Result<i64, AuditError> {
        let conn = self.conn.lock().map_err(|_| AuditError::LockPoisoned)?;
        let timestamp = Utc::now().to_rfc3339();

        conn.execute(
            "INSERT INTO events (timestamp, event_type, resource_id, command, payload, duration_ms, status)
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)",
            params![timestamp, event_type, resource_id, command, payload, duration_ms, status],
        )?;

        Ok(conn.last_insert_rowid())
    }

    /// Query recent events, newest first.
    pub fn recent(&self, limit: usize) -> Result<Vec<AuditEvent>, AuditError> {
        let conn = self.conn.lock().map_err(|_| AuditError::LockPoisoned)?;
        let mut stmt = conn.prepare(
            "SELECT id, timestamp, event_type, resource_id, command, payload, duration_ms, status
             FROM events ORDER BY id DESC LIMIT ?1",
        )?;

        let rows = stmt.query_map(params![limit as i64], |row| {
            Ok(AuditEvent {
                id: row.get(0)?,
                timestamp: row.get(1)?,
                event_type: row.get(2)?,
                resource_id: row.get(3)?,
                command: row.get(4)?,
                payload: row.get(5)?,
                duration_ms: row.get(6)?,
                status: row.get(7)?,
            })
        })?;

        let mut events = Vec::new();
        for row in rows {
            events.push(row?);
        }
        Ok(events)
    }

    /// Query events for a specific resource.
    pub fn by_resource(
        &self,
        resource_id: &str,
        limit: usize,
    ) -> Result<Vec<AuditEvent>, AuditError> {
        let conn = self.conn.lock().map_err(|_| AuditError::LockPoisoned)?;
        let mut stmt = conn.prepare(
            "SELECT id, timestamp, event_type, resource_id, command, payload, duration_ms, status
             FROM events WHERE resource_id = ?1 ORDER BY id DESC LIMIT ?2",
        )?;

        let rows = stmt.query_map(params![resource_id, limit as i64], |row| {
            Ok(AuditEvent {
                id: row.get(0)?,
                timestamp: row.get(1)?,
                event_type: row.get(2)?,
                resource_id: row.get(3)?,
                command: row.get(4)?,
                payload: row.get(5)?,
                duration_ms: row.get(6)?,
                status: row.get(7)?,
            })
        })?;

        let mut events = Vec::new();
        for row in rows {
            events.push(row?);
        }
        Ok(events)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_record_and_query() {
        let tmp = tempfile::tempdir().unwrap();
        let db_path = tmp.path().join("test_audit.db");
        let audit = AuditLog::open(db_path).unwrap();

        audit
            .record(
                "workspace_created",
                Some("ws-1"),
                Some("devpod up my-repo"),
                Some(r#"{"source":"git"}"#),
                Some(5000),
                "success",
            )
            .unwrap();

        audit
            .record(
                "provider_added",
                Some("docker"),
                Some("devpod provider add docker"),
                None,
                Some(1200),
                "success",
            )
            .unwrap();

        let recent = audit.recent(10).unwrap();
        assert_eq!(recent.len(), 2);
        // Newest first
        assert_eq!(recent[0].event_type, "provider_added");
        assert_eq!(recent[1].event_type, "workspace_created");
    }

    #[test]
    fn test_query_by_resource() {
        let tmp = tempfile::tempdir().unwrap();
        let db_path = tmp.path().join("test_audit.db");
        let audit = AuditLog::open(db_path).unwrap();

        audit.record("workspace_created", Some("ws-1"), None, None, None, "success").unwrap();
        audit.record("workspace_started", Some("ws-1"), None, None, None, "success").unwrap();
        audit.record("workspace_created", Some("ws-2"), None, None, None, "success").unwrap();

        let ws1_events = audit.by_resource("ws-1", 10).unwrap();
        assert_eq!(ws1_events.len(), 2);

        let ws2_events = audit.by_resource("ws-2", 10).unwrap();
        assert_eq!(ws2_events.len(), 1);
    }
}
```

- [ ] **Step 2: Verify it compiles and tests pass**

```bash
cd desktop-new/src-tauri
cargo test persistence::audit::tests
```

Expected: Both tests pass.

- [ ] **Step 3: Commit**

```bash
git add desktop-new/src-tauri/src/persistence/audit.rs
git commit -m "feat(persistence): implement SQLite audit log"
```

---

### Task 4: Wire persistence into Tauri app

**Files:**
- Modify: `desktop-new/src-tauri/src/main.rs`

- [ ] **Step 1: Initialize LogStore and AuditLog at startup**

Add to the setup hook in `main.rs`, after the watcher initialization:

```rust
use persistence::audit::AuditLog;
use persistence::logs::LogStore;
```

In the setup closure, after watcher start:

```rust
            // Initialize persistence
            let log_store = LogStore::new(LogStore::default_dir(), 30)
                .expect("failed to initialize log store");
            let audit_log = AuditLog::open(AuditLog::default_path())
                .expect("failed to initialize audit log");

            // Prune old logs on startup
            if let Ok(pruned) = log_store.prune() {
                if pruned > 0 {
                    info!("Pruned {} old workspace log files", pruned);
                }
            }

            app.manage(Arc::new(log_store));
            app.manage(Arc::new(audit_log));
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
git commit -m "feat(persistence): wire LogStore and AuditLog into Tauri app"
```
