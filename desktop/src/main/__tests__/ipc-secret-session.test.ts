// @vitest-environment node
import { EventEmitter } from "node:events"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { ProviderJobs } from "../provider-jobs.js"
import { WorkspaceJobs } from "../workspace-jobs.js"

type Handler = (...args: unknown[]) => unknown
const handlers = new Map<string, Handler>()

vi.mock("electron", () => ({
  app: {
    getPath: () => "/tmp",
    getAppPath: () => "/tmp",
    getVersion: () => "0.0.0",
    isPackaged: false,
  },
  dialog: {},
  ipcMain: {
    handle: (channel: string, handler: Handler) =>
      handlers.set(channel, handler),
    on: () => undefined,
  },
}))

vi.mock("../analytics.js", () => ({
  hashWorkspaceRef: (value: string) => value,
  trackEvent: () => undefined,
}))

const { registerIpcHandlers } = await import("../ipc.js")

function setup(timeoutMs = 5 * 60 * 1000) {
  const webContents = new EventEmitter() as EventEmitter & {
    send: ReturnType<typeof vi.fn>
    isDestroyed: () => boolean
  }
  webContents.send = vi.fn()
  webContents.isDestroyed = () => false

  const win = new EventEmitter() as EventEmitter & {
    webContents: typeof webContents
    isDestroyed: () => boolean
    isMinimized: () => boolean
    isVisible: () => boolean
    restore: ReturnType<typeof vi.fn>
    show: ReturnType<typeof vi.fn>
    focus: ReturnType<typeof vi.fn>
  }
  let minimized = true
  let visible = false
  win.webContents = webContents
  win.isDestroyed = () => false
  win.isMinimized = () => minimized
  win.isVisible = () => visible
  win.restore = vi.fn(() => {
    minimized = false
  })
  win.show = vi.fn(() => {
    visible = true
  })
  win.focus = vi.fn()

  let unlock!: () => Promise<string | undefined>
  const cli = {
    setUnlockHandler: (handler: () => Promise<string | undefined>) => {
      unlock = handler
    },
    run: vi.fn(async () => ({})),
    runRaw: vi.fn(async () => ""),
    hasSessionPassphrase: () => false,
  }
  registerIpcHandlers({
    cli,
    state: { workspaceContext: () => "ctx", providerList: () => [] },
    logStore: {
      createLogFile: () => "/tmp/log",
      appendLog: () => true,
      closeLog: async () => undefined,
      onDrain: async () => undefined,
    },
    pty: { cancelFor: vi.fn(async () => undefined) },
    getMainWindow: () => win,
    providerJobs: new ProviderJobs(),
    workspaceJobs: new WorkspaceJobs(),
    secretSessionTimeoutMs: timeoutMs,
  } as unknown as Parameters<typeof registerIpcHandlers>[0])
  return { win, webContents, unlock }
}

describe("secret unlock IPC lifecycle", () => {
  beforeEach(() => handlers.clear())

  it("shows and focuses a hidden window, re-notifies joiners, and replays when the renderer is ready", async () => {
    const { win, webContents, unlock } = setup()
    const first = unlock()
    const second = unlock()
    expect(webContents.send).toHaveBeenCalledTimes(2)
    expect(win.restore).toHaveBeenCalledOnce()
    expect(win.show).toHaveBeenCalledOnce()
    expect(win.focus).toHaveBeenCalledTimes(2)

    const ready = handlers.get("app_ready")
    expect(ready).toBeDefined()
    ready?.({ sender: webContents })
    await new Promise((resolve) => setImmediate(resolve))
    expect(
      webContents.send.mock.calls.filter(
        ([channel]) => channel === "secret_unlock_required",
      ),
    ).toHaveLength(3)

    const submitted = handlers.get("secret_unlock_submit")
    await submitted?.({}, { passphrase: "private" })
    await expect(first).resolves.toBe("private")
    await expect(second).resolves.toBe("private")
    expect(webContents.listenerCount("did-start-navigation")).toBe(0)
    expect(webContents.listenerCount("render-process-gone")).toBe(0)
    expect(webContents.listenerCount("destroyed")).toBe(0)
    expect(win.listenerCount("closed")).toBe(0)
  })

  it("ignores in-page and subframe navigation, then cancels a main-frame reload", async () => {
    const { webContents, unlock } = setup()
    let settled = false
    const pending = unlock().then((value) => {
      settled = true
      return value
    })
    webContents.emit("did-start-navigation", {}, "app://home", true, true)
    webContents.emit("did-start-navigation", {}, "https://child", false, false)
    await Promise.resolve()
    expect(settled).toBe(false)

    webContents.emit("did-start-navigation", {}, "app://reload", false, true)
    await expect(pending).resolves.toBeUndefined()
    expect(webContents.listenerCount("did-start-navigation")).toBe(0)
  })

  it.each(["render-process-gone", "destroyed", "closed"] as const)(
    "cancels and cleans up on %s",
    async (eventName) => {
      const { win, webContents, unlock } = setup()
      const pending = unlock()
      if (eventName === "closed") win.emit(eventName)
      else webContents.emit(eventName)
      await expect(pending).resolves.toBeUndefined()
      expect(webContents.listenerCount("did-start-navigation")).toBe(0)
      expect(webContents.listenerCount("render-process-gone")).toBe(0)
      expect(webContents.listenerCount("destroyed")).toBe(0)
      expect(win.listenerCount("closed")).toBe(0)
    },
  )

  it("times out an unready renderer and removes every lifecycle listener", async () => {
    vi.useFakeTimers()
    const { win, webContents, unlock } = setup(250)
    const pending = unlock()
    await vi.advanceTimersByTimeAsync(250)
    await expect(pending).resolves.toBeUndefined()
    expect(webContents.listenerCount("did-start-navigation")).toBe(0)
    expect(webContents.listenerCount("render-process-gone")).toBe(0)
    expect(webContents.listenerCount("destroyed")).toBe(0)
    expect(win.listenerCount("closed")).toBe(0)
    vi.useRealTimers()
  })
})
