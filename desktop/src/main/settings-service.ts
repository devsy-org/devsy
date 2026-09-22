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
  private queue: Promise<unknown> = Promise.resolve()

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
    const update = this.queue.catch(() => {}).then(() => this.applyUpdate(patch))
    this.queue = update.catch(() => {})
    return update
  }

  private async applyUpdate(
    patch: Partial<AppSettings>,
  ): Promise<SettingsUpdateResult> {
    const current = this.deps.store.get()
    const next = patchAppSettings(current, patch)
    let startup: AutostartApplyResult = this.status().startup
    const startupRelevant =
      next.runAtStartup !== current.runAtStartup ||
      next.openToTrayOnStartup !== current.openToTrayOnStartup
    if (startupRelevant) {
      startup = await this.deps.applyAutostart(next)
      if (!startup.applied) {
        // A failed platform application changes nothing: keep and broadcast
        // the current settings; the error detail stays in startup.
        const result = { settings: current, startup }
        this.deps.onChanged(result)
        return result
      }
      if (next.runAtStartup && !startup.enabled) {
        // Persist and broadcast the reverted state so Settings and the tray
        // converge on Run at startup off; the denial detail stays in startup.
        const reverted = {
          ...next,
          runAtStartup: false,
          openToTrayOnStartup: false,
        }
        await this.persistWithAutostartRollback(reverted, current)
        const result = { settings: reverted, startup }
        this.deps.onChanged(result)
        return result
      }
    }
    await this.persistWithAutostartRollback(next, current, startupRelevant)
    const result = { settings: next, startup }
    this.deps.onChanged(result)
    return result
  }

  private async persistWithAutostartRollback(
    target: AppSettings,
    current: AppSettings,
    startupRelevant = true,
  ): Promise<void> {
    try {
      this.deps.store.save(target)
    } catch (err) {
      if (startupRelevant) {
        // The platform login item may already reflect the failed change, so
        // reapply the previous settings to keep it agreeing with the store.
        let rollback: AutostartApplyResult | undefined
        let rollbackError: unknown
        try {
          rollback = await this.deps.applyAutostart(current)
        } catch (err2) {
          rollbackError = err2
        }
        const restored =
          rollbackError === undefined &&
          rollback?.applied === true &&
          rollback.enabled === current.runAtStartup
        if (!restored) {
          const reason =
            rollbackError !== undefined
              ? describe(rollbackError)
              : `${rollback?.status}${rollback?.detail ? ` (${rollback.detail})` : ""}`
          throw new Error(
            `failed to persist settings: ${describe(err)}; autostart rollback also failed: ${reason}`,
          )
        }
      }
      throw err
    }
  }
}
