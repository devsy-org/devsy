use crate::daemon::cli::CliRunner;
use std::sync::Arc;
use tauri::{Manager, State};

#[tauri::command]
pub async fn app_ready(app: tauri::AppHandle) -> Result<(), String> {
    // Close splash screen and show main window
    if let Some(splash) = app.get_webview_window("splash") {
        let _ = splash.close();
    }
    if let Some(main) = app.get_webview_window("main") {
        main.show().map_err(|e| e.to_string())?;
    }
    Ok(())
}

#[tauri::command]
pub async fn devpod_version(cli: State<'_, Arc<CliRunner>>) -> Result<String, String> {
    cli.run_raw(&["version"]).await.map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn devpod_upgrade(
    cli: State<'_, Arc<CliRunner>>,
    version: String,
) -> Result<String, String> {
    cli.run_raw(&["upgrade", "--version", &version])
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn devpod_upgrade_dry_run(
    cli: State<'_, Arc<CliRunner>>,
    version: String,
) -> Result<String, String> {
    cli.run_raw(&["upgrade", "--version", &version, "--dry-run"])
        .await
        .map_err(|e| e.to_string())
}
