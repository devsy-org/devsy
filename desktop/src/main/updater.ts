import { readFileSync, renameSync, writeFileSync } from "node:fs"
import { join } from "node:path"
import { app, type BrowserWindow } from "electron"
import { trackEvent } from "./analytics.js"

export type ReleaseChannel = "stable" | "beta"

export type UpdateStateValue =
  | "idle"
  | "checking"
  | "available"
  | "downloading"
  | "downloaded"
  | "not-available"
  | "error"

export type UpdateErrorCode =
  | "dev-mode"
  | "unsupported"
  | "network"
  | "feed-error"
  | "verification"

export interface UpdateProgress {
  percent: number
  bytesPerSecond: number
  transferred: number
  total: number
}

export interface UpdateStatus {
  state: UpdateStateValue
  version?: string
  releaseNotes?: string
  releaseName?: string
  progress?: UpdateProgress
  error?: string
  code?: UpdateErrorCode
}

interface PersistedSettings {
  channel?: ReleaseChannel
  autoDownload?: boolean
}

function settingsPath(): string {
  return join(app.getPath("userData"), "update-settings.json")
}

function loadSettings(): PersistedSettings {
  try {
    return JSON.parse(readFileSync(settingsPath(), "utf-8")) as PersistedSettings
  } catch {
    return {}
  }
}

function saveSettings(patch: PersistedSettings): void {
  try {
    const current = loadSettings()
    const target = settingsPath()
    const tmp = `${target}.tmp`
    writeFileSync(tmp, JSON.stringify({ ...current, ...patch }))
    renameSync(tmp, target)
  } catch (err) {
    console.warn("[updater] failed to persist settings:", err)
  }
}

let currentChannel: ReleaseChannel = "stable"
let autoDownloadEnabled = true
let getMainWindowFn: (() => BrowserWindow | null) | null = null
let lastStatus: UpdateStatus = { state: "idle" }

function sendUpdateStatus(status: UpdateStatus): void {
  const win = getMainWindowFn?.()
  if (win && !win.isDestroyed()) {
    win.webContents.send("update-status", status)
  }
}

function setStatus(status: UpdateStatus): void {
  lastStatus = status
  sendUpdateStatus(status)
}

export function getLastStatus(): UpdateStatus {
  return lastStatus
}

function normalizeReleaseNotes(
  notes: string | { note: string }[] | null | undefined,
): string | undefined {
  if (!notes) return undefined
  if (typeof notes === "string") return notes
  if (Array.isArray(notes)) return notes.map((n) => n.note).join("\n")
  return undefined
}

function classifyError(err: Error): UpdateErrorCode {
  const m = err.message.toLowerCase()
  if (m.includes("net::") || m.includes("network") || m.includes("enotfound")) return "network"
  if (m.includes("sha512") || m.includes("checksum") || m.includes("integrity")) return "verification"
  return "feed-error"
}

export function setReleaseChannel(channel: ReleaseChannel): void {
  currentChannel = channel
  saveSettings({ channel })
}

export function getReleaseChannel(): ReleaseChannel {
  return currentChannel
}

export function setAutoDownloadEnabled(enabled: boolean): void {
  autoDownloadEnabled = enabled
  saveSettings({ autoDownload: enabled })
  // Update the live autoUpdater too so the change takes effect this session.
  // electron-updater reads autoDownload at the moment update-available fires.
  if (app.isPackaged) {
    import("electron-updater")
      .then(({ autoUpdater }) => {
        if (autoUpdater && typeof autoUpdater === "object") {
          autoUpdater.autoDownload = enabled
        }
      })
      .catch(() => {})
  }
}

export function getAutoDownloadEnabled(): boolean {
  return autoDownloadEnabled
}

export async function initAutoUpdater(
  getMainWindow: () => BrowserWindow | null,
): Promise<void> {
  getMainWindowFn = getMainWindow

  const settings = loadSettings()
  currentChannel = settings.channel ?? "stable"
  autoDownloadEnabled = settings.autoDownload ?? true

  if (!app.isPackaged) {
    setStatus({ state: "not-available", code: "dev-mode" })
    return
  }

  const { autoUpdater } = await import("electron-updater")

  if (!autoUpdater || typeof autoUpdater.checkForUpdates !== "function") {
    setStatus({
      state: "error",
      code: "unsupported",
      error: "Updates require a packaged build",
    })
    return
  }

  autoUpdater.autoDownload = autoDownloadEnabled
  autoUpdater.autoInstallOnAppQuit = true
  autoUpdater.allowPrerelease = currentChannel === "beta"
  autoUpdater.channel = currentChannel === "beta" ? "beta" : "latest"

  autoUpdater.on("checking-for-update", () => {
    trackEvent("update_check")
    setStatus({ state: "checking" })
  })

  autoUpdater.on("update-available", (info) => {
    trackEvent("update_available", { version: info.version })
    setStatus({
      state: "available",
      version: info.version,
      releaseName: info.releaseName ?? undefined,
      releaseNotes: normalizeReleaseNotes(info.releaseNotes),
    })
  })

  autoUpdater.on("update-not-available", (info) => {
    setStatus({
      state: "not-available",
      version: info.version,
    })
  })

  autoUpdater.on("download-progress", (info) => {
    setStatus({
      ...lastStatus,
      state: "downloading",
      progress: {
        percent: info.percent,
        bytesPerSecond: info.bytesPerSecond,
        transferred: info.transferred,
        total: info.total,
      },
    })
  })

  autoUpdater.on("update-downloaded", (info) => {
    trackEvent("update_downloaded", { version: info.version })
    setStatus({
      state: "downloaded",
      version: info.version,
      releaseName: info.releaseName ?? undefined,
      releaseNotes: normalizeReleaseNotes(info.releaseNotes),
    })
  })

  autoUpdater.on("error", (err) => {
    trackEvent("update_error", { error_type: err.name })
    setStatus({
      state: "error",
      code: classifyError(err),
      error: err.message,
    })
    console.error("Auto-update error:", err.message)
  })

  setTimeout(() => {
    autoUpdater.checkForUpdates().catch((err: Error) => {
      console.error("Update check failed:", err.message)
    })
  }, 10_000)
}

async function getUpdater() {
  if (!app.isPackaged) {
    setStatus({ state: "not-available", code: "dev-mode" })
    return null
  }
  const { autoUpdater } = await import("electron-updater")
  if (!autoUpdater || typeof autoUpdater.checkForUpdates !== "function") {
    setStatus({
      state: "error",
      code: "unsupported",
      error: "Updates require a packaged build",
    })
    return null
  }
  return autoUpdater
}

export async function checkForUpdates(): Promise<void> {
  const autoUpdater = await getUpdater()
  if (!autoUpdater) return
  await autoUpdater.checkForUpdates()
}

export async function checkForUpdatesWithChannel(channel: ReleaseChannel): Promise<void> {
  // Caller (set_release_channel IPC) already persisted the channel choice.
  // Just reconfigure the running autoUpdater and kick off a check.
  currentChannel = channel
  const autoUpdater = await getUpdater()
  if (!autoUpdater) return
  autoUpdater.allowPrerelease = channel === "beta"
  autoUpdater.channel = channel === "beta" ? "beta" : "latest"
  await autoUpdater.checkForUpdates()
}

export async function downloadUpdate(): Promise<void> {
  const autoUpdater = await getUpdater()
  if (!autoUpdater) return
  await autoUpdater.downloadUpdate()
}

export async function installUpdate(): Promise<void> {
  const autoUpdater = await getUpdater()
  if (!autoUpdater || typeof autoUpdater.quitAndInstall !== "function") return
  autoUpdater.quitAndInstall()
}
