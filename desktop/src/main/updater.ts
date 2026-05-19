import { readFileSync, writeFileSync } from "node:fs"
import { join } from "node:path"
import { app, dialog, type BrowserWindow } from "electron"
import { trackEvent } from "./analytics.js"

export type ReleaseChannel = "stable" | "beta"

function settingsPath(): string {
  return join(app.getPath("userData"), "update-settings.json")
}

function loadChannel(): ReleaseChannel {
  try {
    const data = JSON.parse(readFileSync(settingsPath(), "utf-8"))
    if (data.channel === "beta") return "beta"
  } catch {
    // File doesn't exist or is corrupt
  }
  return "stable"
}

function saveChannel(channel: ReleaseChannel): void {
  writeFileSync(settingsPath(), JSON.stringify({ channel }))
}

let currentChannel: ReleaseChannel = "stable"

export function setReleaseChannel(channel: ReleaseChannel): void {
  currentChannel = channel
  saveChannel(channel)
}

export function getReleaseChannel(): ReleaseChannel {
  return currentChannel
}

export async function initAutoUpdater(
  getMainWindow: () => BrowserWindow | null,
): Promise<void> {
  currentChannel = loadChannel()
  const { autoUpdater } = await import("electron-updater")

  autoUpdater.autoDownload = true
  autoUpdater.autoInstallOnAppQuit = true
  autoUpdater.allowPrerelease = currentChannel === "beta"
  autoUpdater.channel = currentChannel === "beta" ? "beta" : "latest"

  autoUpdater.on("checking-for-update", () => {
    trackEvent("update_check")
  })

  autoUpdater.on("update-available", (info) => {
    trackEvent("update_available", { version: info.version })
  })

  autoUpdater.on("update-downloaded", (info) => {
    trackEvent("update_downloaded", { version: info.version })

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
          trackEvent("update_installed", { version: info.version })
          autoUpdater.quitAndInstall()
        }
      })
  })

  autoUpdater.on("error", (err) => {
    trackEvent("update_error", { error_type: err.name })
    console.error("Auto-update error:", err.message)
  })

  setTimeout(() => {
    autoUpdater.checkForUpdates().catch((err: Error) => {
      console.error("Update check failed:", err.message)
    })
  }, 10_000)
}

export async function checkForUpdatesWithChannel(
  channel: ReleaseChannel,
): Promise<void> {
  const { autoUpdater } = await import("electron-updater")
  currentChannel = channel
  saveChannel(channel)
  autoUpdater.allowPrerelease = channel === "beta"
  autoUpdater.channel = channel === "beta" ? "beta" : "latest"
  await autoUpdater.checkForUpdates()
}
