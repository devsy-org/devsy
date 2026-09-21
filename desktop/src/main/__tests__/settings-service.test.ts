import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { AppSettingsStore, DEFAULT_APP_SETTINGS } from "../app-settings.js"
import type { AutostartApplyResult } from "../autostart.js"
import { SettingsService } from "../settings-service.js"

const enabled: AutostartApplyResult = { applied: true, enabled: true, status: "enabled" }
const disabled: AutostartApplyResult = { applied: true, enabled: false, status: "disabled" }

describe("SettingsService", () => {
  let dir: string
  let store: AppSettingsStore

  beforeEach(() => {
    dir = mkdtempSync(join(tmpdir(), "settings-service-"))
    store = new AppSettingsStore(join(dir, "app-settings.json"))
    store.load()
  })

  afterEach(() => {
    rmSync(dir, { recursive: true, force: true })
  })

  function service(applyAutostart: (s: typeof DEFAULT_APP_SETTINGS) => Promise<AutostartApplyResult>) {
    const onChanged = vi.fn()
    const svc = new SettingsService({
      store,
      applyAutostart,
      currentAutostartEnabled: () => false,
      onChanged,
    })
    return { svc, onChanged }
  }

  it("applies autostart and persists when enabling succeeds", async () => {
    const { svc, onChanged } = service(async () => enabled)
    const result = await svc.update({ runAtStartup: true })
    expect(result.settings.runAtStartup).toBe(true)
    expect(store.get().runAtStartup).toBe(true)
    expect(onChanged).toHaveBeenCalledTimes(1)
  })

  it("does not persist run-at-startup when the platform denies it", async () => {
    const { svc, onChanged } = service(async () => ({
      applied: false,
      enabled: false,
      status: "denied",
      detail: "denied by the system",
    }))
    const result = await svc.update({ runAtStartup: true })
    expect(result.settings.runAtStartup).toBe(false)
    expect(result.settings.openToTrayOnStartup).toBe(false)
    expect(result.startup.status).toBe("denied")
    expect(store.get().runAtStartup).toBe(false)
    expect(onChanged).not.toHaveBeenCalled()
  })

  it("keeps autostart off when enabling errors", async () => {
    const { svc } = service(async () => ({
      applied: false,
      enabled: false,
      status: "error",
      detail: "boom",
    }))
    const result = await svc.update({ runAtStartup: true, openToTrayOnStartup: true })
    expect(result.settings.runAtStartup).toBe(false)
    expect(store.get().runAtStartup).toBe(false)
  })

  it("reapplies autostart when the dependent toggle changes", async () => {
    const calls: boolean[] = []
    const { svc } = service(async (s) => {
      calls.push(s.openToTrayOnStartup)
      return enabled
    })
    await svc.update({ runAtStartup: true })
    await svc.update({ openToTrayOnStartup: true })
    expect(calls).toEqual([false, true])
    expect(store.get().openToTrayOnStartup).toBe(true)
  })

  it("does not touch autostart for unrelated changes", async () => {
    const apply = vi.fn(async () => enabled)
    const { svc } = service(apply)
    await svc.update({ trayNotifications: "all" })
    expect(apply).not.toHaveBeenCalled()
    expect(store.get().trayNotifications).toBe("all")
  })

  it("disables autostart when run-at-startup turns off", async () => {
    const { svc } = service(async (s) => (s.runAtStartup ? enabled : disabled))
    await svc.update({ runAtStartup: true, openToTrayOnStartup: true })
    const result = await svc.update({ runAtStartup: false })
    expect(result.settings).toEqual({
      runAtStartup: false,
      openToTrayOnStartup: false,
      trayNotifications: "failures",
    })
    expect(result.startup.status).toBe("disabled")
  })
})
