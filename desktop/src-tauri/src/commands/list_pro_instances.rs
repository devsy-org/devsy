use tauri::AppHandle;
use crate::resource_watcher::ProInstance;

use super::{
    config::{CommandConfig, DevsyCommandConfig, DevsyCommandError},
    constants::{DEVSY_BINARY_NAME, DEVSY_COMMAND_LIST, DEVSY_COMMAND_PRO, FLAG_OUTPUT_JSON},
};

pub struct ListProInstancesCommand {}
impl ListProInstancesCommand {
    pub fn new() -> Self {
        ListProInstancesCommand {}
    }

    fn deserialize(&self, d: Vec<u8>) -> Result<Vec<ProInstance>, DevsyCommandError> {
        serde_json::from_slice(&d).map_err(DevsyCommandError::Parse)
    }
}
impl DevsyCommandConfig<Vec<ProInstance>> for ListProInstancesCommand {
    fn config(&self) -> CommandConfig<'_> {
        CommandConfig {
            binary_name: DEVSY_BINARY_NAME,
            args: vec![DEVSY_COMMAND_PRO, DEVSY_COMMAND_LIST, FLAG_OUTPUT_JSON],
        }
    }

    fn exec_blocking(self, app_handle: &AppHandle) -> Result<Vec<ProInstance>, DevsyCommandError> {
        let cmd = self.new_command(app_handle)?;

        let output = tauri::async_runtime::block_on(async move { cmd.output().await })
            .map_err(|_| DevsyCommandError::Output)?;

        self.deserialize(output.stdout)
    }
}
impl ListProInstancesCommand {
    pub async fn exec(
        self,
        app_handle: &AppHandle,
    ) -> Result<Vec<ProInstance>, DevsyCommandError> {
        let cmd = self.new_command(app_handle)?;

        let output = cmd.output().await.map_err(|_| DevsyCommandError::Output)?;

        self.deserialize(output.stdout)
    }
}
