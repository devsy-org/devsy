import { readFileSync, renameSync, writeFileSync } from "node:fs"
import type { AppSettings, TrayNotificationLevel } from "../shared/app-settings.js"

export type { AppSettings, TrayNotificationLevel }

export const DEFAULT_APP_SETTINGS: AppSettings = {
  runAtStartup: false,
  openToTrayOnStartup: false,
  trayNotifications: "failures",
}

const LEVELS: readonly TrayNotificationLevel[] = ["off", "failures", "all"]

// Open-to-tray only exists as a modifier of an automatic startup launch, so
// it can never stay on while run-at-startup is off.
export function normalizeAppSettings(raw: unknown): AppSettings {
  const input = (
    typeof raw === "object" && raw !== null ? raw : {}
  ) as Record<string, unknown>
  const runAtStartup = input.runAtStartup === true
  const openToTrayOnStartup = runAtStartup && input.openToTrayOnStartup === true
  const trayNotifications = LEVELS.includes(
    input.trayNotifications as TrayNotificationLevel,
  )
    ? (input.trayNotifications as TrayNotificationLevel)
    : DEFAULT_APP_SETTINGS.trayNotifications
  return { runAtStartup, openToTrayOnStartup, trayNotifications }
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
    this.settings = normalizeAppSettings(next)
    try {
      const tmp = `${this.filePath}.tmp`
      writeFileSync(tmp, JSON.stringify(this.settings, null, 2))
      renameSync(tmp, this.filePath)
    } catch (err) {
      console.warn("[app-settings] failed to persist settings:", err)
    }
    for (const listener of this.listeners) listener()
  }

  onChange(listener: () => void): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }
}
