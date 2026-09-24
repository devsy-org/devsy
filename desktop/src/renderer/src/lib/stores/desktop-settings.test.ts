import { get } from "svelte/store"
import { describe, expect, it, vi } from "vitest"
import type { AppSettingsState } from "$shared/app-settings.js"

const ipc = vi.hoisted(() => ({
  setAppSettings: vi.fn(),
  getAppSettings: vi.fn(),
  listener: undefined as ((state: AppSettingsState) => void) | undefined,
}))

vi.mock("$lib/ipc/commands.js", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  setAppSettings: ipc.setAppSettings,
  getAppSettings: ipc.getAppSettings,
}))
vi.mock("$lib/ipc/events.js", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  onAppSettingsChanged: (listener: (state: AppSettingsState) => void) => {
    ipc.listener = listener
    return Promise.resolve(() => {})
  },
}))

import {
  initDesktopSettingsListener,
  runAtStartup,
  updateDesktopSettings,
} from "./settings.js"

function appState(enabled: boolean): AppSettingsState {
  return {
    settings: {
      runAtStartup: enabled,
      openToTrayOnStartup: true,
      trayNotifications: "failures",
      desktopLogLevel: "info",
      cliCaptureLogLevel: "info",
    },
    startup: {
      applied: true,
      enabled,
      status: enabled ? "enabled" : "disabled",
    },
  }
}

describe("updateDesktopSettings", () => {
  it("keeps a settings event that lands before the write reply", async () => {
    await initDesktopSettingsListener()
    let reply: (state: AppSettingsState) => void = () => {}
    ipc.setAppSettings.mockReturnValueOnce(
      new Promise<AppSettingsState>((resolve) => {
        reply = resolve
      }),
    )

    const update = updateDesktopSettings({ runAtStartup: false })
    await vi.waitFor(() => expect(ipc.setAppSettings).toHaveBeenCalled())
    ipc.listener?.(appState(true))
    reply(appState(false))
    await update

    expect(get(runAtStartup)).toBe(true)
  })
})
