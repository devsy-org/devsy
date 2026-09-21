import {
  type AppSettings,
  type AppSettingsStore,
  patchAppSettings,
} from "./app-settings.js"
import type { AutostartApplyResult } from "./autostart.js"

export interface SettingsUpdateResult {
  settings: AppSettings
  startup: AutostartApplyResult
}

export interface SettingsServiceDeps {
  store: AppSettingsStore
  applyAutostart: (settings: AppSettings) => Promise<AutostartApplyResult>
  currentAutostartEnabled: () => boolean | undefined
  onChanged: (result: SettingsUpdateResult) => void
}

export class SettingsService {
  constructor(private deps: SettingsServiceDeps) {}

  get(): AppSettings {
    return this.deps.store.get()
  }

  status(): SettingsUpdateResult {
    const enabled = this.deps.currentAutostartEnabled()
    return {
      settings: this.get(),
      startup: {
        applied: enabled !== undefined,
        enabled: enabled === true,
        status:
          enabled === undefined
            ? "unavailable"
            : enabled
              ? "enabled"
              : "disabled",
      },
    }
  }

  async update(patch: Partial<AppSettings>): Promise<SettingsUpdateResult> {
    const current = this.deps.store.get()
    const next = patchAppSettings(current, patch)
    let startup: AutostartApplyResult = this.status().startup
    const startupRelevant =
      next.runAtStartup !== current.runAtStartup ||
      next.openToTrayOnStartup !== current.openToTrayOnStartup
    if (startupRelevant) {
      startup = await this.deps.applyAutostart(next)
      // A denied or failed enable must not persist: Settings and the tray
      // keep showing Run at startup off instead of claiming it is on.
      if (next.runAtStartup && !startup.enabled) {
        const reverted = { ...next, runAtStartup: false, openToTrayOnStartup: false }
        return { settings: reverted, startup }
      }
    }
    this.deps.store.save(next)
    const result = { settings: next, startup }
    this.deps.onChanged(result)
    return result
  }
}
