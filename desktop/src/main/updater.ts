import { readFileSync, writeFileSync } from "node:fs"
import { join } from "node:path"
import { app, dialog, type BrowserWindow } from "electron"
import { trackEvent } from "./analytics.js"

export type ReleaseChannel = "stable" | "beta"

export interface UpdateStatus {
  state: "checking" | "available" | "not-available" | "downloading" | "downloaded" | "error"
  version?: string
  releaseNotes?: string
  releaseName?: string
  error?: string
}

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
let getMainWindowFn: (() => BrowserWindow | null) | null = null

function sendUpdateStatus(status: UpdateStatus): void {
  const win = getMainWindowFn?.()
  if (win && !win.isDestroyed()) {
    win.webContents.send("update-status", status)
  }
}

function normalizeReleaseNotes(
  notes: string | { note: string }[] | null | undefined,
): string | undefined {
  if (!notes) return undefined
  if (typeof notes === "string") return notes
  if (Array.isArray(notes)) return notes.map((n) => n.note).join("\n")
  return undefined
}

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
  getMainWindowFn = getMainWindow
  currentChannel = loadChannel()
  const { autoUpdater } = await import("electron-updater")

  autoUpdater.autoDownload = true
  autoUpdater.autoInstallOnAppQuit = true
  autoUpdater.allowPrerelease = currentChannel === "beta"
  autoUpdater.channel = currentChannel === "beta" ? "beta" : "latest"

  autoUpdater.on("checking-for-update", () => {
    trackEvent("update_check")
    sendUpdateStatus({ state: "checking" })
  })

  autoUpdater.on("update-available", (info) => {
    trackEvent("update_available", { version: info.version })
    sendUpdateStatus({
      state: "available",
      version: info.version,
      releaseName: info.releaseName ?? undefined,
      releaseNotes: normalizeReleaseNotes(info.releaseNotes),
    })
  })

  autoUpdater.on("update-not-available", (info) => {
    sendUpdateStatus({
      state: "not-available",
      version: info.version,
    })
  })

  autoUpdater.on("update-downloaded", (info) => {
    trackEvent("update_downloaded", { version: info.version })
    sendUpdateStatus({
      state: "downloaded",
      version: info.version,
      releaseName: info.releaseName ?? undefined,
      releaseNotes: normalizeReleaseNotes(info.releaseNotes),
    })

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
    sendUpdateStatus({ state: "error", error: err.message })
    console.error("Auto-update error:", err.message)
  })

  setTimeout(() => {
    autoUpdater.checkForUpdates().catch((err: Error) => {
      console.error("Update check failed:", err.message)
    })
  }, 10_000)
}

export async function checkForUpdates(): Promise<void> {
  const { autoUpdater } = await import("electron-updater")
  await autoUpdater.checkForUpdates()
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

export async function installUpdate(): Promise<void> {
  const { autoUpdater } = await import("electron-updater")
  autoUpdater.quitAndInstall()
}
