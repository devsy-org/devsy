import { beforeEach, describe, expect, it, vi } from "vitest"

const electronUpdaterMock = {
  autoUpdater: {
    autoDownload: true,
    autoInstallOnAppQuit: true,
    allowPrerelease: false,
    channel: "latest",
    handlers: new Map<string, (...args: unknown[]) => void>(),
    on(event: string, cb: (...args: unknown[]) => void) {
      this.handlers.set(event, cb)
      return this
    },
    emit(event: string, ...args: unknown[]) {
      this.handlers.get(event)?.(...args)
    },
    checkForUpdates: vi.fn().mockResolvedValue(undefined),
    downloadUpdate: vi.fn().mockResolvedValue(undefined),
    quitAndInstall: vi.fn(),
  },
}

vi.mock("electron-updater", () => ({
  ...electronUpdaterMock,
  default: electronUpdaterMock,
}))
vi.mock("electron", () => ({
  app: {
    isPackaged: true,
    getPath: () => "/tmp/devsy-test",
    getVersion: () => "1.0.0",
  },
  dialog: { showMessageBox: vi.fn() },
}))
vi.mock("../analytics.js", () => ({ trackEvent: vi.fn() }))

describe("updater", () => {
  beforeEach(async () => {
    electronUpdaterMock.autoUpdater.handlers.clear()
    electronUpdaterMock.autoUpdater.checkForUpdates.mockClear()
    electronUpdaterMock.autoUpdater.downloadUpdate.mockClear()
    electronUpdaterMock.autoUpdater.quitAndInstall.mockReset()
    vi.resetModules()
    // Restore isPackaged on every test so an early throw in one test
    // cannot silently flip later tests into the dev-mode branch.
    const electron = await import("electron")
    ;(electron.app as { isPackaged: boolean }).isPackaged = true
  })

  it("emits dev-mode status when app is not packaged", async () => {
    const electron = await import("electron")
    ;(electron.app as { isPackaged: boolean }).isPackaged = false
    const { initAutoUpdater } = await import("../updater.js")
    const send = vi.fn()
    const win = { isDestroyed: () => false, webContents: { send } } as never
    await initAutoUpdater(() => win)
    expect(send).toHaveBeenCalledWith(
      "update-status",
      expect.objectContaining({ state: "not-available", code: "dev-mode" }),
    )
  })

  it("emits downloading state with progress info", async () => {
    const { initAutoUpdater } = await import("../updater.js")
    const send = vi.fn()
    const win = { isDestroyed: () => false, webContents: { send } } as never
    await initAutoUpdater(() => win)
    electronUpdaterMock.autoUpdater.emit("download-progress", {
      percent: 42,
      bytesPerSecond: 1000,
      transferred: 100,
      total: 200,
    })
    expect(send).toHaveBeenCalledWith(
      "update-status",
      expect.objectContaining({
        state: "downloading",
        progress: {
          percent: 42,
          bytesPerSecond: 1000,
          transferred: 100,
          total: 200,
        },
      }),
    )
  })

  it("respects autoDownload setting on update-available", async () => {
    const { initAutoUpdater, setAutoDownloadEnabled } = await import(
      "../updater.js"
    )
    const send = vi.fn()
    const win = { isDestroyed: () => false, webContents: { send } } as never
    await initAutoUpdater(() => win)
    setAutoDownloadEnabled(false)
    electronUpdaterMock.autoUpdater.emit("update-available", {
      version: "9.9.9",
    })
    expect(
      electronUpdaterMock.autoUpdater.downloadUpdate,
    ).not.toHaveBeenCalled()
  })

  it("marks the app as quitting before quitAndInstall", async () => {
    const { installUpdate } = await import("../updater.js")
    const { isAppQuitting } = await import("../app-lifecycle.js")
    await installUpdate()
    expect(isAppQuitting()).toBe(true)
    expect(
      electronUpdaterMock.autoUpdater.quitAndInstall,
    ).toHaveBeenCalledTimes(1)
  })

  it("restores lifecycle state when quitAndInstall fails", async () => {
    electronUpdaterMock.autoUpdater.quitAndInstall.mockImplementation(() => {
      throw new Error("install failed")
    })
    const { getLastStatus, initAutoUpdater, installUpdate } = await import(
      "../updater.js"
    )
    const { isAppQuitting } = await import("../app-lifecycle.js")
    const send = vi.fn()
    const win = { isDestroyed: () => false, webContents: { send } } as never
    await initAutoUpdater(() => win)
    electronUpdaterMock.autoUpdater.emit("update-downloaded", {
      version: "9.9.9",
    })

    await expect(installUpdate()).rejects.toThrow("install failed")
    expect(isAppQuitting()).toBe(false)
    expect(getLastStatus()).toMatchObject({
      state: "error",
      code: "install-failed",
      version: "9.9.9",
    })
  })

  it("continues notifying update listeners after one throws", async () => {
    const { initAutoUpdater, onUpdateStatusChanged } = await import(
      "../updater.js"
    )
    const send = vi.fn()
    const win = { isDestroyed: () => false, webContents: { send } } as never
    await initAutoUpdater(() => win)
    const first = vi.fn(() => {
      throw new Error("listener failed")
    })
    const second = vi.fn()
    onUpdateStatusChanged(first)
    onUpdateStatusChanged(second)

    expect(() =>
      electronUpdaterMock.autoUpdater.emit("update-available", {
        version: "9.9.9",
      }),
    ).not.toThrow()
    expect(first).toHaveBeenCalled()
    expect(second).toHaveBeenCalled()
  })

  it("swallows a channel-missing rejection from check_for_updates", async () => {
    electronUpdaterMock.autoUpdater.checkForUpdates.mockRejectedValueOnce(
      new Error(
        'Cannot find channel "latest-mac.yml" update info: HttpError: 404',
      ),
    )
    const { checkForUpdates } = await import("../updater.js")
    await expect(checkForUpdates()).resolves.toBeUndefined()
  })

  it("propagates non-channel-missing errors from check_for_updates", async () => {
    electronUpdaterMock.autoUpdater.checkForUpdates.mockRejectedValueOnce(
      new Error("net::ERR_INTERNET_DISCONNECTED"),
    )
    const { checkForUpdates } = await import("../updater.js")
    await expect(checkForUpdates()).rejects.toThrow("ERR_INTERNET_DISCONNECTED")
  })

  it("does not fire a native dialog on update-downloaded", async () => {
    const electron = await import("electron")
    const { initAutoUpdater } = await import("../updater.js")
    const send = vi.fn()
    const win = { isDestroyed: () => false, webContents: { send } } as never
    await initAutoUpdater(() => win)
    electronUpdaterMock.autoUpdater.emit("update-downloaded", {
      version: "9.9.9",
    })
    expect(
      electron.dialog.showMessageBox as ReturnType<typeof vi.fn>,
    ).not.toHaveBeenCalled()
  })

  it("checks at boot and again on the recheck interval", async () => {
    vi.useFakeTimers()
    try {
      const { initAutoUpdater, stopAutoUpdater } = await import("../updater.js")
      const send = vi.fn()
      const win = { isDestroyed: () => false, webContents: { send } } as never
      await initAutoUpdater(() => win)

      expect(
        electronUpdaterMock.autoUpdater.checkForUpdates,
      ).not.toHaveBeenCalled()
      await vi.advanceTimersByTimeAsync(10_000)
      expect(
        electronUpdaterMock.autoUpdater.checkForUpdates,
      ).toHaveBeenCalledTimes(1)

      await vi.advanceTimersByTimeAsync(6 * 60 * 60 * 1000)
      expect(
        electronUpdaterMock.autoUpdater.checkForUpdates,
      ).toHaveBeenCalledTimes(2)

      stopAutoUpdater()
      await vi.advanceTimersByTimeAsync(6 * 60 * 60 * 1000)
      expect(
        electronUpdaterMock.autoUpdater.checkForUpdates,
      ).toHaveBeenCalledTimes(2)
    } finally {
      vi.useRealTimers()
    }
  })

  it("clears the pending boot check when stopped before it fires", async () => {
    vi.useFakeTimers()
    try {
      const { initAutoUpdater, stopAutoUpdater } = await import("../updater.js")
      const send = vi.fn()
      const win = { isDestroyed: () => false, webContents: { send } } as never
      await initAutoUpdater(() => win)

      // Quit before the 10s boot delay elapses.
      stopAutoUpdater()
      await vi.advanceTimersByTimeAsync(10_000)
      expect(
        electronUpdaterMock.autoUpdater.checkForUpdates,
      ).not.toHaveBeenCalled()
    } finally {
      vi.useRealTimers()
    }
  })

  it("skips the background check while a download is in flight", async () => {
    vi.useFakeTimers()
    try {
      const { initAutoUpdater, stopAutoUpdater } = await import("../updater.js")
      const send = vi.fn()
      const win = { isDestroyed: () => false, webContents: { send } } as never
      await initAutoUpdater(() => win)

      await vi.advanceTimersByTimeAsync(10_000)
      expect(
        electronUpdaterMock.autoUpdater.checkForUpdates,
      ).toHaveBeenCalledTimes(1)

      electronUpdaterMock.autoUpdater.emit("update-downloaded", {
        version: "9.9.9",
      })
      await vi.advanceTimersByTimeAsync(6 * 60 * 60 * 1000)
      expect(
        electronUpdaterMock.autoUpdater.checkForUpdates,
      ).toHaveBeenCalledTimes(1)

      stopAutoUpdater()
    } finally {
      vi.useRealTimers()
    }
  })
})
