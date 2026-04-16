use crate::daemon::cli::CliRunner;
use std::sync::Arc;
use tauri::State;

#[tauri::command]
pub async fn devpod_version(cli: State<'_, Arc<CliRunner>>) -> Result<String, String> {
    cli.run_raw(&["version"]).await.map_err(|e| e.to_string())
}
