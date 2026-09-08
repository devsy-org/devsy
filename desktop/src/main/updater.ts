import { readFileSync, renameSync, writeFileSync } from "node:fs"
import { join } from "node:path"
import { app, type BrowserWindow } from "electron"
import type { AppUpdater } from "electron-updater"
import semver from "semver"
import { trackEvent } from "./analytics.js"

export type ReleaseChannel = "stable" | "beta"

export type UpdateStateValue =
  | "idle"
  | "checking"
  | "available"
  | "downloading"
  | "downloaded"
  | "up-to-date"
  | "not-available"
  | "error"

export type UpdateErrorCode =
  | "dev-mode"
  | "unsupported"
  | "network"
  | "feed-error"
  | "verification"
  | "channel-missing"

export type CandidateResult =
  | { kind: "newer"; version: string }
  | { kind: "same"; version: string }
  | { kind: "older"; version: string }
  | { kind: "invalid"; version: string }

export function classifyCandidate(
  currentVersion: string,
  candidateVersion: string,
): CandidateResult {
  const current = semver.clean(currentVersion) ?? semver.valid(currentVersion)
  const candidate = semver.clean(candidateVersion) ?? semver.valid(candidateVersion)

  if (!current || !candidate) {
    return { kind: "invalid", version: candidateVersion }
  }

  const diff = semver.compare(candidate, current)
  if (diff > 0) {
    return { kind: "newer", version: candidate }
  }
  if (diff === 0) {
    return { kind: "same", version: candidate }
  }
  return { kind: "older", version: candidate }
}

export function configureUpdaterChannel(
  autoUpdater: AppUpdater,
  channel: ReleaseChannel,
): void {
  autoUpdater.allowPrerelease = channel === "beta"
  autoUpdater.channel = channel === "beta" ? "beta" : "latest"
  autoUpdater.allowDowngrade = false
}

export type UpdateDecisionResult =
  | "newer"
  | "same"
  | "feed-behind"
  | "invalid-version"
  | "download-started"
  | "downloaded"
  | "error"

export interface UpdateDecisionLog {
  currentVersion: string
  feedVersion?: string
  availableVersion?: string
  channel: ReleaseChannel
  result: UpdateDecisionResult
  error?: string
}

export function logUpdateDecision(params: UpdateDecisionLog): void {
  const parts = [
    `[updater] check result:`,
    `current=${params.currentVersion}`,
    params.feedVersion ? `feed=${params.feedVersion}` : null,
    params.availableVersion ? `available=${params.availableVersion}` : null,
    `channel=${params.channel}`,
    `result=${params.result}`,
    params.error ? `error=${params.error}` : null,
  ].filter(Boolean)
  console.info(parts.join(" "))
}
export interface UpdateProgress {
  percent: number
  bytesPerSecond: number
  transferred: number
  total: number
}
export type UpdateStatus =
  | {
      state: "idle"
      currentVersion: string
      version?: string
    }
  | {
      state: "checking"
      currentVersion: string
      version?: string
    }
  | {
      state: "up-to-date" | "not-available"
      currentVersion: string
      version?: string
      lastCheckedAt?: number
      feedVersion?: string
      code?: UpdateErrorCode
    }
  | {
      state: "available"
      currentVersion: string
      availableVersion: string
      version?: string
      releaseNotes?: string
      releaseName?: string
      code?: UpdateErrorCode
    }
  | {
      state: "downloading"
      currentVersion: string
      availableVersion: string
      version?: string
      progress: UpdateProgress
    }
  | {
      state: "downloaded"
      currentVersion: string
      availableVersion: string
      version?: string
      releaseNotes?: string
      releaseName?: string
    }
  | {
      state: "error"
      currentVersion: string
      version?: string
      code: UpdateErrorCode
      error: string
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

// Delay before the first check so it doesn't compete with app startup, then
// re-check on a fixed interval so long-running sessions still discover releases
// published after launch.
const INITIAL_CHECK_DELAY_MS = 10_000
const RECHECK_INTERVAL_MS = 6 * 60 * 60 * 1000

function getCurrentVersion(): string {
  try {
    return app.getVersion()
  } catch {
    return ""
  }
}

let currentChannel: ReleaseChannel = "stable"
let autoDownloadEnabled = true
let getMainWindowFn: (() => BrowserWindow | null) | null = null
let lastStatus: UpdateStatus = { state: "idle", currentVersion: "" }
let initialCheckTimer: ReturnType<typeof setTimeout> | null = null
let recheckTimer: ReturnType<typeof setInterval> | null = null

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
  notes: string | { note: string | null }[] | null | undefined,
): string | undefined {
  if (!notes) return undefined
  if (typeof notes === "string") return notes
  if (Array.isArray(notes))
    return notes
      .map((n) => n.note)
      .filter((n): n is string => !!n)
      .join("\n")
  return undefined
}

function classifyError(err: Error): UpdateErrorCode {
  const m = err.message.toLowerCase()
  if (m.includes("cannot find channel") || (m.includes("404") && m.includes(".yml"))) {
    return "channel-missing"
  }
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
    loadAutoUpdater()
      .then((autoUpdater) => {
        if (autoUpdater) {
          autoUpdater.autoDownload = enabled
        }
      })
      .catch(() => {})
  }
}

// electron-updater exposes `autoUpdater` via a CJS getter that Node's
// cjs-module-lexer doesn't surface as a named export under ESM. We have to
// reach it through `default` (the CJS module.exports), which invokes the
// getter and returns the platform-specific updater instance.
async function loadAutoUpdater(): Promise<
  (typeof import("electron-updater"))["autoUpdater"] | null
> {
  const mod = await import("electron-updater")
  return mod.default?.autoUpdater ?? mod.autoUpdater ?? null
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
    setStatus({
      state: "up-to-date",
      currentVersion: getCurrentVersion(),
      code: "dev-mode",
    })
    return
  }

  const autoUpdater = await loadAutoUpdater()

  if (!autoUpdater || typeof autoUpdater.checkForUpdates !== "function") {
    setStatus({
      state: "error",
      currentVersion: getCurrentVersion(),
      code: "unsupported",
      error: "Updates require a packaged build",
    })
    return
  }

  autoUpdater.autoDownload = autoDownloadEnabled
  autoUpdater.autoInstallOnAppQuit = true
  configureUpdaterChannel(autoUpdater, currentChannel)

  autoUpdater.on("checking-for-update", () => {
    trackEvent("update_check")
    setStatus({
      state: "checking",
      currentVersion: getCurrentVersion(),
    })
  })

  autoUpdater.on("update-available", (info) => {
    const currentVersion = getCurrentVersion()
    const candidate = classifyCandidate(currentVersion, info.version)

    if (candidate.kind !== "newer") {
      const result: UpdateDecisionResult =
        candidate.kind === "older"
          ? "feed-behind"
          : candidate.kind === "same"
            ? "same"
            : "invalid-version"
      logUpdateDecision({
        currentVersion,
        feedVersion: info.version,
        channel: currentChannel,
        result,
      })
      // autoDownload is honored by electron-updater at the moment this event
      // fires, so a rejected candidate would still be fetched. Cancel it.
      autoUpdater.autoDownload = false
      setStatus({
        state: "up-to-date",
        currentVersion,
        feedVersion: info.version,
        version: info.version,
      })
      return
    }

    // Restore user preference before a legitimate candidate downloads.
    autoUpdater.autoDownload = autoDownloadEnabled

    logUpdateDecision({
      currentVersion,
      feedVersion: info.version,
      availableVersion: info.version,
      channel: currentChannel,
      result: "newer",
    })
    trackEvent("update_available", { version: info.version })
    setStatus({
      state: "available",
      currentVersion,
      availableVersion: info.version,
      version: info.version,
      releaseName: info.releaseName ?? undefined,
      releaseNotes: normalizeReleaseNotes(info.releaseNotes),
    })
  })

  autoUpdater.on("update-not-available", (info) => {
    const currentVersion = getCurrentVersion()
    logUpdateDecision({
      currentVersion,
      feedVersion: info.version,
      channel: currentChannel,
      result: "same",
    })
    setStatus({
      state: "up-to-date",
      currentVersion,
      feedVersion: info.version,
      version: info.version,
    })
  })

  autoUpdater.on("download-progress", (info) => {
    if (lastStatus.state !== "available" && lastStatus.state !== "downloading") {
      return
    }
    const availableVersion = lastStatus.availableVersion
    setStatus({
      state: "downloading",
      currentVersion: getCurrentVersion(),
      availableVersion,
      version: availableVersion,
      progress: {
        percent: info.percent,
        bytesPerSecond: info.bytesPerSecond,
        transferred: info.transferred,
        total: info.total,
      },
    })
  })

  autoUpdater.on("update-downloaded", (info) => {
    if (lastStatus.state !== "available" && lastStatus.state !== "downloading") {
      return
    }
    const availableVersion = lastStatus.availableVersion
    const currentVersion = getCurrentVersion()
    logUpdateDecision({
      currentVersion,
      availableVersion,
      channel: currentChannel,
      result: "downloaded",
    })
    setStatus({
      state: "downloaded",
      currentVersion,
      availableVersion,
      version: availableVersion,
      releaseName: info.releaseName ?? undefined,
      releaseNotes: normalizeReleaseNotes(info.releaseNotes),
    })
  })
  autoUpdater.on("error", (err) => {
    const code = classifyError(err)
    const currentVersion = getCurrentVersion()
    logUpdateDecision({
      currentVersion,
      channel: currentChannel,
      result: "error",
      error: err.message,
    })
    trackEvent("update_error", { error_type: err.name })
    if (code === "channel-missing") {
      setStatus({
        state: "up-to-date",
        currentVersion,
        code,
      })
      console.warn("Auto-update: channel manifest missing:", err.message)
      return
    }
    setStatus({
      state: "error",
      currentVersion,
      code,
      error: err.message,
    })
    console.error("Auto-update error:", err.message)
  })

  const runBackgroundCheck = (): void => {
    // Skip while a download is already in flight or staged — re-fetching the
    // manifest would just churn state. autoUpdater.autoDownload is already set
    // from the user's preference, so a plain check honors that toggle.
    if (lastStatus.state === "downloading" || lastStatus.state === "downloaded") {
      return
    }
    autoUpdater.checkForUpdates().catch((err: Error) => {
      console.error("Update check failed:", err.message)
    })
  }

  initialCheckTimer = setTimeout(runBackgroundCheck, INITIAL_CHECK_DELAY_MS)
  recheckTimer = setInterval(runBackgroundCheck, RECHECK_INTERVAL_MS)
}

export function stopAutoUpdater(): void {
  if (initialCheckTimer) {
    clearTimeout(initialCheckTimer)
    initialCheckTimer = null
  }
  if (recheckTimer) {
    clearInterval(recheckTimer)
    recheckTimer = null
  }
}

async function getUpdater() {
  if (!app.isPackaged) {
    setStatus({
      state: "up-to-date",
      currentVersion: getCurrentVersion(),
      code: "dev-mode",
    })
    return null
  }
  const autoUpdater = await loadAutoUpdater()
  if (!autoUpdater || typeof autoUpdater.checkForUpdates !== "function") {
    setStatus({
      state: "error",
      currentVersion: getCurrentVersion(),
      code: "unsupported",
      error: "Updates require a packaged build",
    })
    return null
  }
  return autoUpdater
}

async function runUpdateCheck(
  autoUpdater: NonNullable<Awaited<ReturnType<typeof getUpdater>>>,
): Promise<void> {
  try {
    await autoUpdater.checkForUpdates()
  } catch (err) {
    if (err instanceof Error && classifyError(err) === "channel-missing") return
    throw err
  }
}

export async function checkForUpdates(): Promise<void> {
  const autoUpdater = await getUpdater()
  if (!autoUpdater) return
  await runUpdateCheck(autoUpdater)
}

export async function checkForUpdatesWithChannel(channel: ReleaseChannel): Promise<void> {
  // Caller (set_release_channel IPC) already persisted the channel choice.
  // Just reconfigure the running autoUpdater and kick off a check.
  currentChannel = channel
  const autoUpdater = await getUpdater()
  if (!autoUpdater) return
  configureUpdaterChannel(autoUpdater, channel)
  await runUpdateCheck(autoUpdater)
}

export async function downloadUpdate(): Promise<void> {
  if (lastStatus.state !== "available") return
  const currentVersion = getCurrentVersion()
  const candidateVersion = lastStatus.availableVersion ?? lastStatus.version ?? ""
  if (classifyCandidate(currentVersion, candidateVersion).kind !== "newer") {
    return
  }
  logUpdateDecision({
    currentVersion,
    availableVersion: candidateVersion,
    channel: currentChannel,
    result: "download-started",
  })
  const autoUpdater = await getUpdater()
  if (!autoUpdater) return
  await autoUpdater.downloadUpdate()
}

export async function installUpdate(): Promise<void> {
  if (lastStatus.state !== "downloaded") return
  const autoUpdater = await getUpdater()
  if (!autoUpdater || typeof autoUpdater.quitAndInstall !== "function") return
  ;(app as typeof app & { isQuitting?: boolean }).isQuitting = true
  autoUpdater.quitAndInstall()
}
