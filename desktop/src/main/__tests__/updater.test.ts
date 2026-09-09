import { beforeEach, describe, expect, it, vi } from "vitest"

let mockAppVersion = "1.0.0"
let mockAllowPrerelease = false
let mockChannel = "latest"

const electronUpdaterMock = {
  autoUpdater: {
    autoDownload: true,
    autoInstallOnAppQuit: true,
    get allowPrerelease() {
      return mockAllowPrerelease
    },
    set allowPrerelease(v: boolean) {
      mockAllowPrerelease = v
      if (v) this.allowDowngrade = true
    },
    get channel() {
      return mockChannel
    },
    set channel(v: string) {
      mockChannel = v
      this.allowDowngrade = true
    },
    allowDowngrade: false,
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
    isQuitting: false,
    getPath: () => "/tmp/devsy-test",
    getVersion: () => mockAppVersion,
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
    electronUpdaterMock.autoUpdater.allowDowngrade = false
    mockAllowPrerelease = false
    mockChannel = "latest"
    mockAppVersion = "1.0.0"
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
      expect.objectContaining({ state: "up-to-date", code: "dev-mode" }),
    )
  })

  it("emits downloading state with progress info", async () => {
    const { initAutoUpdater } = await import("../updater.js")
    const send = vi.fn()
    const win = { isDestroyed: () => false, webContents: { send } } as never
    await initAutoUpdater(() => win)
    electronUpdaterMock.autoUpdater.emit("update-available", { version: "2.0.0" })
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

  it("sets app.isQuitting before quitAndInstall so the window can close", async () => {
    const electron = await import("electron")
    ;(
      electron.app as typeof electron.app & { isQuitting?: boolean }
    ).isQuitting = false
    const { initAutoUpdater, installUpdate } = await import("../updater.js")
    const send = vi.fn()
    const win = { isDestroyed: () => false, webContents: { send } } as never
    await initAutoUpdater(() => win)
    electronUpdaterMock.autoUpdater.emit("update-available", { version: "2.0.0" })
    electronUpdaterMock.autoUpdater.emit("update-downloaded", { version: "2.0.0" })

    let quittingWhenInstalled: boolean | undefined
    electronUpdaterMock.autoUpdater.quitAndInstall.mockImplementation(() => {
      quittingWhenInstalled = (
        electron.app as typeof electron.app & { isQuitting?: boolean }
      ).isQuitting
    })
    await installUpdate()
    expect(quittingWhenInstalled).toBe(true)
    expect(
      electronUpdaterMock.autoUpdater.quitAndInstall,
    ).toHaveBeenCalledTimes(1)
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
    electronUpdaterMock.autoUpdater.emit("update-available", { version: "9.9.9" })
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

      electronUpdaterMock.autoUpdater.emit("update-available", { version: "9.9.9" })
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

  it("guards downloadUpdate so it only runs when an update is available and newer", async () => {
    const { initAutoUpdater, downloadUpdate } = await import("../updater.js")
    const send = vi.fn()
    const win = { isDestroyed: () => false, webContents: { send } } as never
    await initAutoUpdater(() => win)

    // Initially idle
    await downloadUpdate()
    expect(electronUpdaterMock.autoUpdater.downloadUpdate).not.toHaveBeenCalled()

    // Available with a newer version
    electronUpdaterMock.autoUpdater.emit("update-available", { version: "1.1.0" })
    await downloadUpdate()
    expect(electronUpdaterMock.autoUpdater.downloadUpdate).toHaveBeenCalledTimes(1)
  })

  it("guards installUpdate so it only runs when status is downloaded", async () => {
    const { initAutoUpdater, installUpdate } = await import("../updater.js")
    const send = vi.fn()
    const win = { isDestroyed: () => false, webContents: { send } } as never
    await initAutoUpdater(() => win)

    // State is idle
    await installUpdate()
    expect(electronUpdaterMock.autoUpdater.quitAndInstall).not.toHaveBeenCalled()
  })

  describe("classifyCandidate", () => {
    it("classifies newer, same, older, and invalid correctly", async () => {
      const { classifyCandidate } = await import("../updater.js")
      expect(classifyCandidate("1.17.0", "1.18.0")).toEqual({ kind: "newer", version: "1.18.0" })
      expect(classifyCandidate("1.17.0", "1.17.1")).toEqual({ kind: "newer", version: "1.17.1" })
      expect(classifyCandidate("1.17.0", "1.17.0")).toEqual({ kind: "same", version: "1.17.0" })
      expect(classifyCandidate("1.17.0", "1.16.2")).toEqual({ kind: "older", version: "1.16.2" })
      expect(classifyCandidate("1.18.0-beta.2", "1.18.0-beta.3")).toEqual({
        kind: "newer",
        version: "1.18.0-beta.3",
      })
      expect(classifyCandidate("1.18.0-beta.2", "1.17.0")).toEqual({
        kind: "older",
        version: "1.17.0",
      })
      expect(classifyCandidate("1.17.0", "garbage")).toEqual({ kind: "invalid", version: "garbage" })
      expect(classifyCandidate("garbage", "1.17.0")).toEqual({ kind: "invalid", version: "1.17.0" })
    })
  })

  describe("candidate validation and #1187 regression", () => {
    it("rejects an older candidate (1.17.0 vs 1.16.2) and reports not-available (#1187)", async () => {
      mockAppVersion = "1.17.0"
      const { initAutoUpdater } = await import("../updater.js")
      const send = vi.fn()
      const win = { isDestroyed: () => false, webContents: { send } } as never
      await initAutoUpdater(() => win)

      electronUpdaterMock.autoUpdater.emit("update-available", { version: "1.16.2" })
      expect(send).toHaveBeenCalledWith(
        "update-status",
        expect.objectContaining({
          state: "up-to-date",
          currentVersion: "1.17.0",
          feedVersion: "1.16.2",
        }),
      )
      expect(send).not.toHaveBeenCalledWith(
        "update-status",
        expect.objectContaining({ state: "available" }),
      )
      expect(electronUpdaterMock.autoUpdater.downloadUpdate).not.toHaveBeenCalled()
    })

    it("cancels autoDownload and ignores download events for rejected candidate", async () => {
      mockAppVersion = "1.17.0"
      const { initAutoUpdater } = await import("../updater.js")
      const send = vi.fn()
      const win = { isDestroyed: () => false, webContents: { send } } as never
      await initAutoUpdater(() => win)

      electronUpdaterMock.autoUpdater.emit("update-available", { version: "1.16.2" })
      expect(electronUpdaterMock.autoUpdater.autoDownload).toBe(false)
      expect(send).toHaveBeenCalledWith(
        "update-status",
        expect.objectContaining({ state: "up-to-date" }),
      )

      electronUpdaterMock.autoUpdater.emit("download-progress", {
        percent: 50,
        bytesPerSecond: 1000,
        transferred: 50,
        total: 100,
      })
      electronUpdaterMock.autoUpdater.emit("update-downloaded", { version: "1.16.2" })

      expect(send).not.toHaveBeenCalledWith(
        "update-status",
        expect.objectContaining({ state: "downloading" }),
      )
      expect(send).not.toHaveBeenCalledWith(
        "update-status",
        expect.objectContaining({ state: "downloaded" }),
      )
    })

    it("treats equal version (1.17.0 vs 1.17.0) as not-available", async () => {
      mockAppVersion = "1.17.0"
      const { initAutoUpdater } = await import("../updater.js")
      const send = vi.fn()
      const win = { isDestroyed: () => false, webContents: { send } } as never
      await initAutoUpdater(() => win)

      electronUpdaterMock.autoUpdater.emit("update-available", { version: "1.17.0" })
      expect(send).toHaveBeenCalledWith(
        "update-status",
        expect.objectContaining({
          state: "up-to-date",
          currentVersion: "1.17.0",
          feedVersion: "1.17.0",
        }),
      )
      expect(send).not.toHaveBeenCalledWith(
        "update-status",
        expect.objectContaining({ state: "available" }),
      )
    })

    it("accepts a newer minor version (1.17.0 vs 1.18.0) as available", async () => {
      mockAppVersion = "1.17.0"
      const { initAutoUpdater } = await import("../updater.js")
      const send = vi.fn()
      const win = { isDestroyed: () => false, webContents: { send } } as never
      await initAutoUpdater(() => win)

      electronUpdaterMock.autoUpdater.emit("update-available", { version: "1.18.0" })
      expect(send).toHaveBeenCalledWith(
        "update-status",
        expect.objectContaining({
          state: "available",
          currentVersion: "1.17.0",
          availableVersion: "1.18.0",
        }),
      )
    })

    it("accepts a newer patch version (1.17.0 vs 1.17.1) as available", async () => {
      mockAppVersion = "1.17.0"
      const { initAutoUpdater } = await import("../updater.js")
      const send = vi.fn()
      const win = { isDestroyed: () => false, webContents: { send } } as never
      await initAutoUpdater(() => win)

      electronUpdaterMock.autoUpdater.emit("update-available", { version: "1.17.1" })
      expect(send).toHaveBeenCalledWith(
        "update-status",
        expect.objectContaining({
          state: "available",
          currentVersion: "1.17.0",
          availableVersion: "1.17.1",
        }),
      )
    })

    it("accepts preview progression (1.18.0-beta.2 vs 1.18.0-beta.3) as available", async () => {
      mockAppVersion = "1.18.0-beta.2"
      const { initAutoUpdater, checkForUpdatesWithChannel } = await import("../updater.js")
      const send = vi.fn()
      const win = { isDestroyed: () => false, webContents: { send } } as never
      await initAutoUpdater(() => win)
      await checkForUpdatesWithChannel("beta")

      electronUpdaterMock.autoUpdater.emit("update-available", { version: "1.18.0-beta.3" })
      expect(send).toHaveBeenCalledWith(
        "update-status",
        expect.objectContaining({
          state: "available",
          currentVersion: "1.18.0-beta.2",
          availableVersion: "1.18.0-beta.3",
        }),
      )
    })

    it("rejects older stable feed after preview switch (1.18.0-beta.2 vs 1.17.0 on stable)", async () => {
      mockAppVersion = "1.18.0-beta.2"
      const { initAutoUpdater, checkForUpdatesWithChannel } = await import("../updater.js")
      const send = vi.fn()
      const win = { isDestroyed: () => false, webContents: { send } } as never
      await initAutoUpdater(() => win)
      await checkForUpdatesWithChannel("stable")

      electronUpdaterMock.autoUpdater.emit("update-available", { version: "1.17.0" })
      expect(send).toHaveBeenCalledWith(
        "update-status",
        expect.objectContaining({ state: "up-to-date" }),
      )
      expect(send).not.toHaveBeenCalledWith(
        "update-status",
        expect.objectContaining({ state: "available" }),
      )
    })

    it("reports feed-error for malformed candidate versions", async () => {
      mockAppVersion = "1.17.0"
      const { initAutoUpdater } = await import("../updater.js")
      const send = vi.fn()
      const win = { isDestroyed: () => false, webContents: { send } } as never
      await initAutoUpdater(() => win)

      electronUpdaterMock.autoUpdater.emit("update-available", { version: "not-a-version" })
      expect(send).toHaveBeenCalledWith(
        "update-status",
        expect.objectContaining({
          state: "error",
          code: "feed-error",
          currentVersion: "1.17.0",
        }),
      )
      expect(send).not.toHaveBeenCalledWith(
        "update-status",
        expect.objectContaining({ state: "up-to-date" }),
      )
      expect(electronUpdaterMock.autoUpdater.downloadUpdate).not.toHaveBeenCalled()
    })

    it("reports feed-error when current app version is malformed", async () => {
      mockAppVersion = "not-a-version"
      const { initAutoUpdater } = await import("../updater.js")
      const send = vi.fn()
      const win = { isDestroyed: () => false, webContents: { send } } as never
      await initAutoUpdater(() => win)

      electronUpdaterMock.autoUpdater.emit("update-available", { version: "1.18.0" })
      expect(send).toHaveBeenCalledWith(
        "update-status",
        expect.objectContaining({
          state: "error",
          code: "feed-error",
          currentVersion: "not-a-version",
        }),
      )
      expect(send).not.toHaveBeenCalledWith(
        "update-status",
        expect.objectContaining({ state: "up-to-date" }),
      )
      expect(electronUpdaterMock.autoUpdater.downloadUpdate).not.toHaveBeenCalled()
    })

    it("enforces allowDowngrade is false across channel configurations", async () => {
      const { initAutoUpdater, checkForUpdatesWithChannel } = await import("../updater.js")
      const win = { isDestroyed: () => false, webContents: { send: vi.fn() } } as never
      await initAutoUpdater(() => win)
      expect(electronUpdaterMock.autoUpdater.allowDowngrade).toBe(false)

      await checkForUpdatesWithChannel("beta")
      expect(electronUpdaterMock.autoUpdater.allowPrerelease).toBe(true)
      expect(electronUpdaterMock.autoUpdater.allowDowngrade).toBe(false)

      await checkForUpdatesWithChannel("stable")
      expect(electronUpdaterMock.autoUpdater.allowPrerelease).toBe(false)
      expect(electronUpdaterMock.autoUpdater.allowDowngrade).toBe(false)
    })

    it("does not populate the legacy version field in published update-status", async () => {
      mockAppVersion = "1.17.0"
      const { initAutoUpdater } = await import("../updater.js")
      const send = vi.fn()
      const win = { isDestroyed: () => false, webContents: { send } } as never
      await initAutoUpdater(() => win)

      electronUpdaterMock.autoUpdater.emit("update-available", { version: "1.18.0" })
      const availableStatus = send.mock.calls[send.mock.calls.length - 1][1] as Record<string, unknown>
      expect(availableStatus.availableVersion).toBe("1.18.0")
      expect(availableStatus.version).toBeUndefined()

      electronUpdaterMock.autoUpdater.emit("update-available", { version: "1.16.2" })
      const upToDateStatus = send.mock.calls[send.mock.calls.length - 1][1] as Record<string, unknown>
      expect(upToDateStatus.feedVersion).toBe("1.16.2")
      expect(upToDateStatus.version).toBeUndefined()
    })
  })

  describe("structured diagnostics logging", () => {
    it("logs check result with current, feed, channel, and result fields", async () => {
      mockAppVersion = "1.17.0"
      const infoSpy = vi.spyOn(console, "info").mockImplementation(() => {})
      const { initAutoUpdater } = await import("../updater.js")
      const win = { isDestroyed: () => false, webContents: { send: vi.fn() } } as never
      await initAutoUpdater(() => win)

      electronUpdaterMock.autoUpdater.emit("update-available", { version: "1.16.2" })
      expect(infoSpy).toHaveBeenCalledWith(
        expect.stringMatching(
          /\[updater\] check result: current=1\.17\.0 feed=1\.16\.2 channel=stable result=feed-behind/,
        ),
      )

      electronUpdaterMock.autoUpdater.emit("update-available", { version: "1.18.0" })
      expect(infoSpy).toHaveBeenCalledWith(
        expect.stringMatching(
          /\[updater\] check result: current=1\.17\.0 feed=1\.18\.0 available=1\.18\.0 channel=stable result=newer/,
        ),
      )

      electronUpdaterMock.autoUpdater.emit("update-not-available", { version: "1.17.0" })
      expect(infoSpy).toHaveBeenCalledWith(
        expect.stringMatching(
          /\[updater\] check result: current=1\.17\.0 feed=1\.17\.0 channel=stable result=same/,
        ),
      )

      infoSpy.mockRestore()
    })
  })
})
