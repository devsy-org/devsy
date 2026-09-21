import {
  type AppSettings,
  type AppSettingsStore,
  patchAppSettings,
} from "./app-settings.js"
import type { AutostartApplyResult } from "./autostart.js"

function describe(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

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
      if (next.runAtStartup && !startup.enabled) {
        // Persist and broadcast the reverted state so Settings and the tray
        // converge on Run at startup off; the denial detail stays in startup.
        const reverted = { ...next, runAtStartup: false, openToTrayOnStartup: false }
        this.deps.store.save(reverted)
        const result = { settings: reverted, startup }
        this.deps.onChanged(result)
        return result
      }
    }
    try {
      this.deps.store.save(next)
    } catch (err) {
      if (startupRelevant) {
        // The OS login item already moved to the new value; restore it so a
        // failed write never leaves the platform disagreeing with the store.
        try {
          await this.deps.applyAutostart(current)
        } catch (compensationErr) {
          throw new Error(
            `failed to persist settings: ${describe(err)}; autostart rollback also failed: ${describe(compensationErr)}`,
          )
        }
      }
      throw err
    }
    const result = { settings: next, startup }
    this.deps.onChanged(result)
    return result
  }
}
