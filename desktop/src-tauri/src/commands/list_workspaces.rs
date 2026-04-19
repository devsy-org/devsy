use tauri::AppHandle;

use crate::resource_watcher::Workspace;

use super::{
    config::{CommandConfig, DevsyCommandConfig, DevsyCommandError},
    constants::{DEVSY_BINARY_NAME, DEVSY_COMMAND_LIST, FLAG_OUTPUT_JSON},
};

pub struct ListWorkspacesCommand {}
impl ListWorkspacesCommand {
    pub fn new() -> Self {
        ListWorkspacesCommand {}
    }

    fn deserialize(&self, d: Vec<u8>) -> Result<Vec<Workspace>, DevsyCommandError> {
        serde_json::from_slice(&d).map_err(DevsyCommandError::Parse)
    }
}
impl DevsyCommandConfig<Vec<Workspace>> for ListWorkspacesCommand {
    fn config(&self) -> CommandConfig<'_> {
        CommandConfig {
            binary_name: DEVSY_BINARY_NAME,
            args: vec![DEVSY_COMMAND_LIST, FLAG_OUTPUT_JSON],
        }
    }

    fn exec_blocking(self, app_handle: &AppHandle) -> Result<Vec<Workspace>, DevsyCommandError> {
        let cmd = self.new_command(app_handle)?;

        let output = tauri::async_runtime::block_on(async move { cmd.output().await })
            .map_err(|_| DevsyCommandError::Output)?;

        self.deserialize(output.stdout)
    }
}

impl ListWorkspacesCommand {
    pub async fn exec(self, app_handle: &AppHandle) -> Result<Vec<Workspace>, DevsyCommandError> {
        let cmd = self.new_command(app_handle)?;

        let output = cmd.output().await.map_err(|_| DevsyCommandError::Output)?;

        self.deserialize(output.stdout)
    }
}
