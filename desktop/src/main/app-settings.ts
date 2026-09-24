import { readFileSync, renameSync, writeFileSync } from "node:fs"
import type {
  AppSettings,
  LogLevel,
  TrayNotificationLevel,
} from "../shared/app-settings.js"

export type { AppSettings, LogLevel, TrayNotificationLevel }

export const DEFAULT_APP_SETTINGS: AppSettings = {
  runAtStartup: false,
  openToTrayOnStartup: false,
  trayNotifications: "failures",
}

const LEVELS: readonly TrayNotificationLevel[] = ["off", "failures", "all"]
const LOG_LEVELS: readonly LogLevel[] = ["error", "warn", "info", "debug", "trace"]

export function normalizeAppSettings(raw: unknown): AppSettings {
  const input = (typeof raw === "object" && raw !== null ? raw : {}) as Record<
    string,
    unknown
  >
  const runAtStartup = input.runAtStartup === true
  const openToTrayOnStartup = runAtStartup && input.openToTrayOnStartup === true
  const trayNotifications = LEVELS.includes(
    input.trayNotifications as TrayNotificationLevel,
  )
    ? (input.trayNotifications as TrayNotificationLevel)
    : DEFAULT_APP_SETTINGS.trayNotifications
  const normalized = { runAtStartup, openToTrayOnStartup, trayNotifications }
  if (LOG_LEVELS.includes(input.logLevel as LogLevel)) {
    return { ...normalized, logLevel: input.logLevel as LogLevel }
  }
  return normalized
}

export function patchAppSettings(
  current: AppSettings,
  patch: Partial<AppSettings>,
): AppSettings {
  return normalizeAppSettings({ ...current, ...patch })
}

// Throws on a mistyped value so a renderer bug cannot silently persist a
// corrupt preference.
export function sanitizeAppSettingsPatch(raw: unknown): Partial<AppSettings> {
  if (!raw || typeof raw !== "object") return {}
  const input = raw as Record<string, unknown>
  const patch: Partial<AppSettings> = {}
  if ("runAtStartup" in input) {
    if (typeof input.runAtStartup !== "boolean")
      throw new Error("runAtStartup must be boolean")
    patch.runAtStartup = input.runAtStartup
  }
  if ("openToTrayOnStartup" in input) {
    if (typeof input.openToTrayOnStartup !== "boolean")
      throw new Error("openToTrayOnStartup must be boolean")
    patch.openToTrayOnStartup = input.openToTrayOnStartup
  }
  if ("trayNotifications" in input) {
    if (!LEVELS.includes(input.trayNotifications as TrayNotificationLevel))
      throw new Error("trayNotifications must be off, failures, or all")
    patch.trayNotifications = input.trayNotifications as TrayNotificationLevel
  }
  if ("logLevel" in input) {
    if (!LOG_LEVELS.includes(input.logLevel as LogLevel))
      throw new Error("logLevel must be error, warn, info, debug, or trace")
    patch.logLevel = input.logLevel as LogLevel
  }
  return patch
}

export class AppSettingsStore {
  private settings: AppSettings
  private listeners = new Set<() => void>()

  constructor(private readonly filePath: string) {
    this.settings = DEFAULT_APP_SETTINGS
  }

  load(): AppSettings {
    let raw: unknown = {}
    try {
      raw = JSON.parse(readFileSync(this.filePath, "utf-8"))
    } catch {
      raw = {}
    }
    this.settings = normalizeAppSettings(raw)
    return this.settings
  }

  get(): AppSettings {
    return this.settings
  }

  save(next: AppSettings): void {
    const normalized = normalizeAppSettings(next)
    const tmp = `${this.filePath}.tmp`
    writeFileSync(tmp, JSON.stringify(normalized, null, 2))
    renameSync(tmp, this.filePath)
    this.settings = normalized
    for (const listener of this.listeners) listener()
  }

  onChange(listener: () => void): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }
}
