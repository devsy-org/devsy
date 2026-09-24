import { readFileSync, renameSync, writeFileSync } from "node:fs"
import { join } from "node:path"
import { app, type BrowserWindow } from "electron"
import electronUpdater, { type AppUpdater } from "electron-updater"
import semver from "semver"
import { trackEvent } from "./analytics.js"
import { clearAppQuitting, markAppQuitting } from "./app-lifecycle.js"
import { mainLog } from "./logging.js"

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
  | "not-eligible"
  | "install-failed"

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
  | "not-eligible"
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
  mainLog.info(parts.join(" "))
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
    }
  | {
      state: "checking"
      currentVersion: string
    }
  | {
      state: "up-to-date" | "not-available"
      currentVersion: string
      lastCheckedAt?: number
      feedVersion?: string
      code?: UpdateErrorCode
    }
  | {
      state: "available"
      currentVersion: string
      availableVersion: string
      releaseNotes?: string
      releaseName?: string
      code?: UpdateErrorCode
    }
  | {
      state: "downloading"
      currentVersion: string
      availableVersion: string
      progress: UpdateProgress
    }
  | {
      state: "downloaded"
      currentVersion: string
      availableVersion: string
      releaseNotes?: string
      releaseName?: string
    }
  | {
      state: "error"
      currentVersion: string
      availableVersion?: string
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
    mainLog.warn("[updater] failed to persist settings:", err)
  }
}

// Delay before the first check so it doesn't compete with app startup, then
// re-check on a fixed interval so long-running sessions still discover releases
// published after launch.
const INITIAL_CHECK_DELAY_MS = 10_000
const RECHECK_INTERVAL_MS = 6 * 60 * 60 * 1000

function getCurrentVersion(): string | null {
  try {
    const v = app.getVersion()
    return v || null
  } catch (err) {
      mainLog.error(
      "Auto-update: unable to read app version:",
      err instanceof Error ? err.message : String(err),
    )
    return null
  }
}

let currentChannel: ReleaseChannel = "stable"
let autoDownloadEnabled = true
let getMainWindowFn: (() => BrowserWindow | null) | null = null
let lastStatus: UpdateStatus = { state: "idle", currentVersion: "" }
let initialCheckTimer: ReturnType<typeof setTimeout> | null = null
let recheckTimer: ReturnType<typeof setInterval> | null = null
let checkInFlight = false
let activeCandidate: UpdateCandidate | null = null
const statusListeners = new Set<(status: UpdateStatus) => void>()

type UpdateCandidate = {
  version: string
  state: "available" | "downloading" | "downloaded"
}

function getAutoUpdater(): AppUpdater {
  return electronUpdater.autoUpdater
}

function sendUpdateStatus(status: UpdateStatus): void {
  const win = getMainWindowFn?.()
  if (win && !win.isDestroyed()) {
    win.webContents.send("update-status", status)
  }
}

function setStatus(status: UpdateStatus): void {
  lastStatus = status
  sendUpdateStatus(status)
  for (const listener of statusListeners) {
    try {
      listener(status)
    } catch (error) {
      mainLog.error("[updater] status listener failed:", error)
    }
  }
}

export function onUpdateStatusChanged(
  listener: (status: UpdateStatus) => void,
): () => void {
  statusListeners.add(listener)
  return () => statusListeners.delete(listener)
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

export function getReleaseChannel(): ReleaseChannel {
  return currentChannel
}

export function setAutoDownloadEnabled(enabled: boolean): void {
  autoDownloadEnabled = enabled
  saveSettings({ autoDownload: enabled })
  if (app.isPackaged) {
    getAutoUpdater().autoDownload = enabled
  }
}

export function getAutoDownloadEnabled(): boolean {
  return autoDownloadEnabled
}

function clearCandidate(): void {
  activeCandidate = null
}

function isChannelSwitchBlocked(): boolean {
  return (
    checkInFlight ||
    activeCandidate?.state === "downloading" ||
    activeCandidate?.state === "downloaded"
  )
}

function channelSwitchBlockedError(): Error {
  return new Error(
    "Cannot switch release channel while an update is being checked, downloaded, or ready to install",
  )
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
      currentVersion: getCurrentVersion() ?? "",
      code: "dev-mode",
    })
    return
  }

  const autoUpdater = getAutoUpdater()
  if (typeof autoUpdater.checkForUpdates !== "function") {
    setStatus({
      state: "error",
      currentVersion: getCurrentVersion() ?? "",
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
      currentVersion: getCurrentVersion() ?? "",
    })
  })

  autoUpdater.on("update-available", (info) => {
    const currentVersion = getCurrentVersion()
    if (!currentVersion) {
      logUpdateDecision({
        currentVersion: "unknown",
        feedVersion: info.version,
        channel: currentChannel,
        result: "invalid-version",
        error: "current version unavailable",
      })
      setStatus({
        state: "error",
        currentVersion: "",
        code: "unsupported",
        error: "Unable to determine current application version",
      })
      clearCandidate()
      return
    }

    const candidate = classifyCandidate(currentVersion, info.version)

    if (candidate.kind === "invalid") {
      logUpdateDecision({
        currentVersion,
        feedVersion: info.version,
        channel: currentChannel,
        result: "invalid-version",
      })
      autoUpdater.autoDownload = false
      setStatus({
        state: "error",
        currentVersion,
        code: "feed-error",
        error: `Invalid version from update feed: ${info.version}`,
      })
      clearCandidate()
      return
    }

    if (candidate.kind !== "newer") {
      const result: UpdateDecisionResult =
        candidate.kind === "older" ? "feed-behind" : "same"
      logUpdateDecision({
        currentVersion,
        feedVersion: info.version,
        channel: currentChannel,
        result,
      })
      autoUpdater.autoDownload = false
      setStatus({
        state: "up-to-date",
        currentVersion,
        feedVersion: info.version,
      })
      clearCandidate()
      return
    }

    autoUpdater.autoDownload = autoDownloadEnabled
    activeCandidate = {
      version: info.version,
      state: autoDownloadEnabled ? "downloading" : "available",
    }

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
      releaseName: info.releaseName ?? undefined,
      releaseNotes: normalizeReleaseNotes(info.releaseNotes),
    })
  })

  autoUpdater.on("update-not-available", (info) => {
    const currentVersion = getCurrentVersion() ?? ""
    const candidate = classifyCandidate(currentVersion, info.version)
    const result: UpdateDecisionResult =
      candidate.kind === "newer"
        ? "not-eligible"
        : candidate.kind === "older"
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
    setStatus({
      state: candidate.kind === "newer" ? "not-available" : "up-to-date",
      currentVersion,
      feedVersion: info.version,
      ...(candidate.kind === "newer" ? { code: "not-eligible" } : {}),
    })
    clearCandidate()
  })

  autoUpdater.on("download-progress", (info) => {
    if (
      !activeCandidate ||
      (lastStatus.state !== "available" && lastStatus.state !== "downloading")
    ) {
      return
    }
    activeCandidate.state = "downloading"
    setStatus({
      state: "downloading",
      currentVersion: getCurrentVersion() ?? "",
      availableVersion: activeCandidate.version,
      progress: {
        percent: info.percent,
        bytesPerSecond: info.bytesPerSecond,
        transferred: info.transferred,
        total: info.total,
      },
    })
  })

  autoUpdater.on("update-downloaded", (info) => {
    if (
      !activeCandidate ||
      info.version !== activeCandidate.version ||
      (lastStatus.state !== "available" && lastStatus.state !== "downloading")
    ) {
      return
    }
    activeCandidate.state = "downloaded"
    trackEvent("update_downloaded", { version: info.version })
    const currentVersion = getCurrentVersion() ?? ""
    logUpdateDecision({
      currentVersion,
      availableVersion: info.version,
      channel: currentChannel,
      result: "downloaded",
    })
    setStatus({
      state: "downloaded",
      currentVersion,
      availableVersion: info.version,
      releaseName: info.releaseName ?? undefined,
      releaseNotes: normalizeReleaseNotes(info.releaseNotes),
    })
  })

  autoUpdater.on("error", (err) => {
    clearCandidate()
    const code = classifyError(err)
    const currentVersion = getCurrentVersion() ?? ""
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
      mainLog.warn("Auto-update: channel manifest missing:", err.message)
      return
    }
    setStatus({
      state: "error",
      currentVersion,
      code,
      error: err.message,
    })
    mainLog.error("Auto-update error:", err.message)
  })

  const runBackgroundCheck = (): void => {
    if (isChannelSwitchBlocked()) {
      return
    }
    checkInFlight = true
    void runUpdateCheck(autoUpdater)
      .catch((err: Error) => {
        mainLog.error("Update check failed:", err.message)
      })
      .finally(() => {
        checkInFlight = false
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

function getUpdater(): AppUpdater | null {
  if (!app.isPackaged) {
    setStatus({
      state: "up-to-date",
      currentVersion: getCurrentVersion() ?? "",
      code: "dev-mode",
    })
    return null
  }
  const autoUpdater = getAutoUpdater()
  if (typeof autoUpdater.checkForUpdates !== "function") {
    setStatus({
      state: "error",
      currentVersion: getCurrentVersion() ?? "",
      code: "unsupported",
      error: "Updates require a packaged build",
    })
    return null
  }
  return autoUpdater
}

async function runUpdateCheck(
  autoUpdater: AppUpdater,
): Promise<void> {
  try {
    await autoUpdater.checkForUpdates()
  } catch (err) {
    if (err instanceof Error && classifyError(err) === "channel-missing") return
    throw err
  }
}

export async function checkForUpdates(): Promise<void> {
  checkInFlight = true
  try {
    const autoUpdater = getUpdater()
    if (!autoUpdater) return
    await runUpdateCheck(autoUpdater)
  } finally {
    checkInFlight = false
  }
}

export async function switchReleaseChannel(channel: ReleaseChannel): Promise<void> {
  if (channel !== currentChannel && isChannelSwitchBlocked()) {
    throw channelSwitchBlockedError()
  }
  checkInFlight = true
  try {
    const autoUpdater = getUpdater()
    if (!autoUpdater) return

    if (channel === currentChannel) {
      await runUpdateCheck(autoUpdater)
      return
    }

    const previousChannel = currentChannel
    currentChannel = channel
    clearCandidate()
    configureUpdaterChannel(autoUpdater, channel)
    try {
      await runUpdateCheck(autoUpdater)
      saveSettings({ channel })
    } catch (error) {
      currentChannel = previousChannel
      configureUpdaterChannel(autoUpdater, previousChannel)
      throw error
    }
  } finally {
    checkInFlight = false
  }
}

export async function downloadUpdate(): Promise<void> {
  if (lastStatus.state !== "available") return
  const currentVersion = getCurrentVersion()
  if (!currentVersion) return
  const candidate = activeCandidate
  if (
    !candidate ||
    candidate.state !== "available" ||
    candidate.version !== lastStatus.availableVersion ||
    classifyCandidate(currentVersion, candidate.version).kind !== "newer"
  ) {
    return
  }
  logUpdateDecision({
    currentVersion,
    availableVersion: candidate.version,
    channel: currentChannel,
    result: "download-started",
  })
  candidate.state = "downloading"
  const autoUpdater = getUpdater()
  if (!autoUpdater) {
    clearCandidate()
    return
  }
  try {
    await autoUpdater.downloadUpdate()
  } catch (error) {
    clearCandidate()
    throw error
  }
}

export async function installUpdate(): Promise<void> {
  const canInstall =
    (lastStatus.state === "downloaded" &&
      activeCandidate?.state === "downloaded" &&
      activeCandidate.version === lastStatus.availableVersion) ||
    (lastStatus.state === "error" && lastStatus.code === "install-failed")
  if (!canInstall) return
  let markedQuitting = false
  try {
    const autoUpdater = getUpdater()
    if (!autoUpdater || typeof autoUpdater.quitAndInstall !== "function") return
    markAppQuitting()
    markedQuitting = true
    autoUpdater.quitAndInstall()
  } catch (error) {
    if (markedQuitting) clearAppQuitting()
    const message = error instanceof Error ? error.message : String(error)
    setStatus({
      ...lastStatus,
      state: "error",
      code: "install-failed",
      error: message,
    })
    mainLog.error("Update installation failed:", message)
    throw error
  }
}
