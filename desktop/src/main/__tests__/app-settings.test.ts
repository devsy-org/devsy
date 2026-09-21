import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { afterEach, beforeEach, describe, expect, it } from "vitest"
import {
  AppSettingsStore,
  DEFAULT_APP_SETTINGS,
  normalizeAppSettings,
  patchAppSettings,
  sanitizeAppSettingsPatch,
} from "../app-settings.js"

describe("normalizeAppSettings", () => {
  it("returns defaults for missing or malformed input", () => {
    expect(normalizeAppSettings(undefined)).toEqual(DEFAULT_APP_SETTINGS)
    expect(normalizeAppSettings(null)).toEqual(DEFAULT_APP_SETTINGS)
    expect(normalizeAppSettings("nope")).toEqual(DEFAULT_APP_SETTINGS)
    expect(normalizeAppSettings({})).toEqual(DEFAULT_APP_SETTINGS)
  })

  it("keeps valid persisted values", () => {
    expect(
      normalizeAppSettings({
        runAtStartup: true,
        openToTrayOnStartup: true,
        trayNotifications: "all",
      }),
    ).toEqual({
      runAtStartup: true,
      openToTrayOnStartup: true,
      trayNotifications: "all",
    })
  })

  it("forces open-to-tray off when run-at-startup is off", () => {
    expect(
      normalizeAppSettings({ runAtStartup: false, openToTrayOnStartup: true }),
    ).toEqual({
      runAtStartup: false,
      openToTrayOnStartup: false,
      trayNotifications: "failures",
    })
  })

  it("falls back to the default notification level for unknown values", () => {
    expect(
      normalizeAppSettings({ trayNotifications: "loud" }).trayNotifications,
    ).toBe("failures")
  })

  it("ignores non-boolean toggles", () => {
    expect(
      normalizeAppSettings({ runAtStartup: "yes" }).runAtStartup,
    ).toBe(false)
  })
})

describe("patchAppSettings", () => {
  it("clears the dependent toggle when run-at-startup is disabled", () => {
    const current = {
      runAtStartup: true,
      openToTrayOnStartup: true,
      trayNotifications: "all" as const,
    }
    expect(patchAppSettings(current, { runAtStartup: false })).toEqual({
      runAtStartup: false,
      openToTrayOnStartup: false,
      trayNotifications: "all",
    })
  })

  it("leaves unrelated settings untouched", () => {
    const current = {
      runAtStartup: true,
      openToTrayOnStartup: false,
      trayNotifications: "off" as const,
    }
    expect(
      patchAppSettings(current, { openToTrayOnStartup: true }),
    ).toEqual({
      runAtStartup: true,
      openToTrayOnStartup: true,
      trayNotifications: "off",
    })
  })
})

describe("sanitizeAppSettingsPatch", () => {
  it("accepts a valid partial patch", () => {
    expect(
      sanitizeAppSettingsPatch({ runAtStartup: true, trayNotifications: "all" }),
    ).toEqual({ runAtStartup: true, trayNotifications: "all" })
  })

  it("returns an empty patch for non-object input", () => {
    expect(sanitizeAppSettingsPatch(undefined)).toEqual({})
    expect(sanitizeAppSettingsPatch("x")).toEqual({})
  })

  it("rejects mistyped values", () => {
    expect(() => sanitizeAppSettingsPatch({ runAtStartup: 1 })).toThrow()
    expect(() =>
      sanitizeAppSettingsPatch({ trayNotifications: "everything" }),
    ).toThrow()
  })
})

describe("AppSettingsStore", () => {
  let dir: string
  let file: string

  beforeEach(() => {
    dir = mkdtempSync(join(tmpdir(), "app-settings-"))
    file = join(dir, "app-settings.json")
  })

  afterEach(() => {
    rmSync(dir, { recursive: true, force: true })
  })

  it("loads defaults when the file does not exist", () => {
    const store = new AppSettingsStore(file)
    expect(store.load()).toEqual(DEFAULT_APP_SETTINGS)
  })

  it("survives a corrupt file", () => {
    writeFileSync(file, "{not json")
    const store = new AppSettingsStore(file)
    expect(store.load()).toEqual(DEFAULT_APP_SETTINGS)
  })

  it("round-trips saved settings", () => {
    const store = new AppSettingsStore(file)
    store.load()
    store.save({
      runAtStartup: true,
      openToTrayOnStartup: true,
      trayNotifications: "off",
    })
    const reloaded = new AppSettingsStore(file)
    expect(reloaded.load()).toEqual({
      runAtStartup: true,
      openToTrayOnStartup: true,
      trayNotifications: "off",
    })
  })

  it("normalizes on save", () => {
    const store = new AppSettingsStore(file)
    store.load()
    store.save({
      runAtStartup: false,
      openToTrayOnStartup: true,
      trayNotifications: "failures",
    })
    expect(JSON.parse(readFileSync(file, "utf-8"))).toEqual({
      runAtStartup: false,
      openToTrayOnStartup: false,
      trayNotifications: "failures",
    })
  })

  it("propagates write failures and keeps the previous settings", () => {
    const store = new AppSettingsStore(join(dir, "missing", "app-settings.json"))
    store.load()
    let calls = 0
    store.onChange(() => calls++)
    expect(() =>
      store.save({ ...DEFAULT_APP_SETTINGS, runAtStartup: true }),
    ).toThrow()
    expect(store.get()).toEqual(DEFAULT_APP_SETTINGS)
    expect(calls).toBe(0)
  })

  it("notifies listeners on save", () => {
    const store = new AppSettingsStore(file)
    store.load()
    let calls = 0
    const off = store.onChange(() => calls++)
    store.save({ ...DEFAULT_APP_SETTINGS, runAtStartup: true })
    expect(calls).toBe(1)
    off()
    store.save({ ...DEFAULT_APP_SETTINGS })
    expect(calls).toBe(1)
  })
})
