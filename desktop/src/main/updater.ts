import { dialog, type BrowserWindow } from "electron"

export async function initAutoUpdater(
  getMainWindow: () => BrowserWindow | null,
): Promise<void> {
  const { autoUpdater } = await import("electron-updater")

  autoUpdater.autoDownload = true
  autoUpdater.autoInstallOnAppQuit = true

  autoUpdater.on("update-downloaded", (info) => {
    const win = getMainWindow()
    if (!win) return

    dialog
      .showMessageBox(win, {
        type: "info",
        title: "Update Ready",
        message: `Version ${info.version} has been downloaded and will be installed on restart.`,
        buttons: ["Restart Now", "Later"],
        defaultId: 0,
        cancelId: 1,
      })
      .then(({ response }) => {
        if (response === 0) {
          autoUpdater.quitAndInstall()
        }
      })
  })

  autoUpdater.on("error", (err) => {
    console.error("Auto-update error:", err.message)
  })

  // Check for updates after a short delay to avoid slowing down app launch
  setTimeout(() => {
    autoUpdater.checkForUpdates().catch((err: Error) => {
      console.error("Update check failed:", err.message)
    })
  }, 10_000)
}
