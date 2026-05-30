import { describe, it, expect, vi, beforeEach } from "vitest"

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
        progress: { percent: 42, bytesPerSecond: 1000, transferred: 100, total: 200 },
      }),
    )
  })

  it("respects autoDownload setting on update-available", async () => {
    const { initAutoUpdater, setAutoDownloadEnabled } = await import("../updater.js")
    const send = vi.fn()
    const win = { isDestroyed: () => false, webContents: { send } } as never
    await initAutoUpdater(() => win)
    setAutoDownloadEnabled(false)
    electronUpdaterMock.autoUpdater.emit("update-available", { version: "9.9.9" })
    expect(electronUpdaterMock.autoUpdater.downloadUpdate).not.toHaveBeenCalled()
  })

  it("does not fire a native dialog on update-downloaded", async () => {
    const electron = await import("electron")
    const { initAutoUpdater } = await import("../updater.js")
    const send = vi.fn()
    const win = { isDestroyed: () => false, webContents: { send } } as never
    await initAutoUpdater(() => win)
    electronUpdaterMock.autoUpdater.emit("update-downloaded", { version: "9.9.9" })
    expect((electron.dialog.showMessageBox as ReturnType<typeof vi.fn>)).not.toHaveBeenCalled()
  })
})
