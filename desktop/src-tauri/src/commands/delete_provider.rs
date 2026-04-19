use tauri::AppHandle;

use super::{
    config::{CommandConfig, DevsyCommandConfig, DevsyCommandError},
    constants::{DEVSY_BINARY_NAME, DEVSY_COMMAND_DELETE, DEVSY_COMMAND_PROVIDER},
};

pub struct DeleteProviderCommand {
    provider_id: String,
}
impl DeleteProviderCommand {
    pub fn new(provider_id: String) -> Self {
        DeleteProviderCommand { provider_id }
    }
}
impl DevsyCommandConfig<()> for DeleteProviderCommand {
    fn config(&self) -> CommandConfig<'_> {
        CommandConfig {
            binary_name: DEVSY_BINARY_NAME,
            args: vec![
                DEVSY_COMMAND_PROVIDER,
                DEVSY_COMMAND_DELETE,
                &self.provider_id,
            ],
        }
    }

    fn exec_blocking(self, app_handle: &AppHandle) -> Result<(), DevsyCommandError> {
        let cmd = self.new_command(app_handle)?;

        tauri::async_runtime::block_on(async move { cmd.status().await })
            .map_err(DevsyCommandError::Failed)?
            .success()
            .then_some(())
            .ok_or_else(|| DevsyCommandError::Exit)
    }
}
