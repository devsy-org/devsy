use tauri::AppHandle;

use super::{
    config::{CommandConfig, DevsyCommandConfig, DevsyCommandError},
    constants::{
        DEVSY_BINARY_NAME, DEVSY_COMMAND_DELETE, DEVSY_COMMAND_PRO, FLAG_IGNORE_NOT_FOUND,
    },
};

pub struct DeleteProInstanceCommand {
    pro_id: String,
}
impl DeleteProInstanceCommand {
    pub fn new(pro_id: String) -> Self {
        DeleteProInstanceCommand { pro_id }
    }
}
impl DevsyCommandConfig<()> for DeleteProInstanceCommand {
    fn config(&self) -> CommandConfig<'_> {
        CommandConfig {
            binary_name: DEVSY_BINARY_NAME,
            args: vec![
                DEVSY_COMMAND_PRO,
                DEVSY_COMMAND_DELETE,
                &self.pro_id,
                FLAG_IGNORE_NOT_FOUND,
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
