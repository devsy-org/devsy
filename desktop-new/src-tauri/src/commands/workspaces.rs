use crate::daemon::cli::{CliRunner, OutputLine};
use crate::daemon::state::DaemonState;
use crate::daemon::types::Workspace;
use crate::events::{event_names, CommandProgressPayload};
use crate::persistence::audit::AuditLog;
use crate::persistence::logs::LogStore;
use log::error;
use std::sync::Arc;
use tauri::{AppHandle, Emitter, State};
use tokio::sync::RwLock;

type SharedState = Arc<RwLock<DaemonState>>;

#[tauri::command]
pub async fn workspace_list(state: State<'_, SharedState>) -> Result<Vec<Workspace>, String> {
    let state = state.read().await;
    Ok(state.workspace_list().into_iter().cloned().collect())
}

#[tauri::command]
pub async fn workspace_up(
    app: AppHandle,
    cli: State<'_, Arc<CliRunner>>,
    log_store: State<'_, Arc<LogStore>>,
    audit: State<'_, Arc<AuditLog>>,
    source: String,
    workspace_id: Option<String>,
    provider: Option<String>,
    ide: Option<String>,
) -> Result<String, String> {
    let mut args = vec!["up".to_string(), source.clone()];

    if let Some(ref id) = workspace_id {
        args.push("--id".to_string());
        args.push(id.clone());
    }
    if let Some(ref p) = provider {
        args.push("--provider".to_string());
        args.push(p.clone());
    }
    if let Some(ref i) = ide {
        args.push("--ide".to_string());
        args.push(i.clone());
    }

    let ws_id = workspace_id.clone().unwrap_or_else(|| source.clone());
    let cmd_id = format!("{:x}", rand_id());
    let cmd_id_clone = cmd_id.clone();

    // Create a log file for this command's output
    let log_path = log_store
        .create_log_file(&ws_id)
        .await
        .map_err(|e| e.to_string())?;

    // Record audit entry
    let audit = audit.inner().clone();
    let audit_ws_id = ws_id.clone();
    if let Err(e) = audit.record("up", "workspace", &audit_ws_id, &source, true) {
        error!("Failed to record audit entry: {}", e);
    }

    let arg_refs: Vec<&str> = args.iter().map(|s| s.as_str()).collect();
    let (tx, mut rx) = tokio::sync::mpsc::channel::<OutputLine>(256);
    let _handle = cli.run_streaming(&arg_refs, tx);

    tokio::spawn(async move {
        while let Some(line) = rx.recv().await {
            match &line {
                OutputLine::Stdout(text) | OutputLine::Stderr(text) => {
                    // Write to log file
                    if let Err(e) = LogStore::append_log(&log_path, text).await {
                        error!("Failed to append to log: {}", e);
                    }
                    let _ = app.emit(
                        event_names::COMMAND_PROGRESS,
                        CommandProgressPayload {
                            command_id: cmd_id_clone.clone(),
                            message: text.clone(),
                            done: false,
                        },
                    );
                }
                OutputLine::Exit(code) => {
                    let exit_msg = format!("Exit code: {}", code);
                    if let Err(e) = LogStore::append_log(&log_path, &exit_msg).await {
                        error!("Failed to append to log: {}", e);
                    }
                    let _ = app.emit(
                        event_names::COMMAND_PROGRESS,
                        CommandProgressPayload {
                            command_id: cmd_id_clone.clone(),
                            message: exit_msg,
                            done: true,
                        },
                    );
                }
            }
        }
    });

    Ok(cmd_id)
}

#[tauri::command]
pub async fn workspace_stop(
    app: AppHandle,
    cli: State<'_, Arc<CliRunner>>,
    log_store: State<'_, Arc<LogStore>>,
    audit: State<'_, Arc<AuditLog>>,
    workspace_id: String,
) -> Result<String, String> {
    let cmd_id = format!("{:x}", rand_id());
    let cmd_id_clone = cmd_id.clone();

    let log_path = log_store
        .create_log_file(&workspace_id)
        .await
        .map_err(|e| e.to_string())?;

    if let Err(e) = audit.record("stop", "workspace", &workspace_id, "", true) {
        error!("Failed to record audit entry: {}", e);
    }

    let (tx, mut rx) = tokio::sync::mpsc::channel::<OutputLine>(256);
    let _handle = cli.run_streaming(&["stop", &workspace_id], tx);

    tokio::spawn(async move {
        while let Some(line) = rx.recv().await {
            match &line {
                OutputLine::Stdout(text) | OutputLine::Stderr(text) => {
                    let _ = LogStore::append_log(&log_path, text).await;
                    let _ = app.emit(
                        event_names::COMMAND_PROGRESS,
                        CommandProgressPayload {
                            command_id: cmd_id_clone.clone(),
                            message: text.clone(),
                            done: false,
                        },
                    );
                }
                OutputLine::Exit(code) => {
                    let exit_msg = format!("Exit code: {}", code);
                    let _ = LogStore::append_log(&log_path, &exit_msg).await;
                    let _ = app.emit(
                        event_names::COMMAND_PROGRESS,
                        CommandProgressPayload {
                            command_id: cmd_id_clone.clone(),
                            message: exit_msg,
                            done: true,
                        },
                    );
                }
            }
        }
    });

    Ok(cmd_id)
}

#[tauri::command]
pub async fn workspace_delete(
    app: AppHandle,
    cli: State<'_, Arc<CliRunner>>,
    log_store: State<'_, Arc<LogStore>>,
    audit: State<'_, Arc<AuditLog>>,
    workspace_id: String,
) -> Result<String, String> {
    let cmd_id = format!("{:x}", rand_id());
    let cmd_id_clone = cmd_id.clone();

    let log_path = log_store
        .create_log_file(&workspace_id)
        .await
        .map_err(|e| e.to_string())?;

    if let Err(e) = audit.record("delete", "workspace", &workspace_id, "", true) {
        error!("Failed to record audit entry: {}", e);
    }

    let (tx, mut rx) = tokio::sync::mpsc::channel::<OutputLine>(256);
    let _handle = cli.run_streaming(&["delete", &workspace_id, "--force"], tx);

    tokio::spawn(async move {
        while let Some(line) = rx.recv().await {
            match &line {
                OutputLine::Stdout(text) | OutputLine::Stderr(text) => {
                    let _ = LogStore::append_log(&log_path, text).await;
                    let _ = app.emit(
                        event_names::COMMAND_PROGRESS,
                        CommandProgressPayload {
                            command_id: cmd_id_clone.clone(),
                            message: text.clone(),
                            done: false,
                        },
                    );
                }
                OutputLine::Exit(code) => {
                    let exit_msg = format!("Exit code: {}", code);
                    let _ = LogStore::append_log(&log_path, &exit_msg).await;
                    let _ = app.emit(
                        event_names::COMMAND_PROGRESS,
                        CommandProgressPayload {
                            command_id: cmd_id_clone.clone(),
                            message: exit_msg,
                            done: true,
                        },
                    );
                }
            }
        }
    });

    Ok(cmd_id)
}

#[tauri::command]
pub async fn workspace_rebuild(
    app: AppHandle,
    cli: State<'_, Arc<CliRunner>>,
    log_store: State<'_, Arc<LogStore>>,
    audit: State<'_, Arc<AuditLog>>,
    workspace_id: String,
) -> Result<String, String> {
    let cmd_id = format!("{:x}", rand_id());
    let cmd_id_clone = cmd_id.clone();

    let log_path = log_store
        .create_log_file(&workspace_id)
        .await
        .map_err(|e| e.to_string())?;

    if let Err(e) = audit.record("rebuild", "workspace", &workspace_id, "", true) {
        error!("Failed to record audit entry: {}", e);
    }

    let (tx, mut rx) = tokio::sync::mpsc::channel::<OutputLine>(256);
    let _handle = cli.run_streaming(&["up", &workspace_id, "--recreate"], tx);

    tokio::spawn(async move {
        while let Some(line) = rx.recv().await {
            match &line {
                OutputLine::Stdout(text) | OutputLine::Stderr(text) => {
                    let _ = LogStore::append_log(&log_path, text).await;
                    let _ = app.emit(
                        event_names::COMMAND_PROGRESS,
                        CommandProgressPayload {
                            command_id: cmd_id_clone.clone(),
                            message: text.clone(),
                            done: false,
                        },
                    );
                }
                OutputLine::Exit(code) => {
                    let exit_msg = format!("Exit code: {}", code);
                    let _ = LogStore::append_log(&log_path, &exit_msg).await;
                    let _ = app.emit(
                        event_names::COMMAND_PROGRESS,
                        CommandProgressPayload {
                            command_id: cmd_id_clone.clone(),
                            message: exit_msg,
                            done: true,
                        },
                    );
                }
            }
        }
    });

    Ok(cmd_id)
}

#[tauri::command]
pub async fn workspace_reset(
    app: AppHandle,
    cli: State<'_, Arc<CliRunner>>,
    log_store: State<'_, Arc<LogStore>>,
    audit: State<'_, Arc<AuditLog>>,
    workspace_id: String,
) -> Result<String, String> {
    let cmd_id = format!("{:x}", rand_id());
    let cmd_id_clone = cmd_id.clone();

    let log_path = log_store
        .create_log_file(&workspace_id)
        .await
        .map_err(|e| e.to_string())?;

    if let Err(e) = audit.record("reset", "workspace", &workspace_id, "", true) {
        error!("Failed to record audit entry: {}", e);
    }

    let (tx, mut rx) = tokio::sync::mpsc::channel::<OutputLine>(256);
    let _handle = cli.run_streaming(&["up", &workspace_id, "--reset"], tx);

    tokio::spawn(async move {
        while let Some(line) = rx.recv().await {
            match &line {
                OutputLine::Stdout(text) | OutputLine::Stderr(text) => {
                    let _ = LogStore::append_log(&log_path, text).await;
                    let _ = app.emit(
                        event_names::COMMAND_PROGRESS,
                        CommandProgressPayload {
                            command_id: cmd_id_clone.clone(),
                            message: text.clone(),
                            done: false,
                        },
                    );
                }
                OutputLine::Exit(code) => {
                    let exit_msg = format!("Exit code: {}", code);
                    let _ = LogStore::append_log(&log_path, &exit_msg).await;
                    let _ = app.emit(
                        event_names::COMMAND_PROGRESS,
                        CommandProgressPayload {
                            command_id: cmd_id_clone.clone(),
                            message: exit_msg,
                            done: true,
                        },
                    );
                }
            }
        }
    });

    Ok(cmd_id)
}

#[tauri::command]
pub async fn workspace_status(
    cli: State<'_, Arc<CliRunner>>,
    workspace_id: String,
) -> Result<String, String> {
    cli.run_raw(&["status", &workspace_id, "--output", "json", "--timeout", "5s"])
        .await
        .map_err(|e| e.to_string())
}

fn rand_id() -> u64 {
    use std::time::{SystemTime, UNIX_EPOCH};
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_nanos() as u64
}
