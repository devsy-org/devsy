// @vitest-environment node
import { EventEmitter } from "node:events"
import { join } from "node:path"
import { pathToFileURL } from "node:url"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { ProviderJobs } from "../provider-jobs.js"
import { WorkspaceJobs } from "../workspace-jobs.js"

type Handler = (...args: unknown[]) => unknown
const handlers = new Map<string, Handler>()

vi.mock("electron", () => ({
  app: {
    getPath: () => "/tmp",
    getAppPath: vi.fn(() => "/tmp"),
    getVersion: () => "0.0.0",
    isPackaged: false,
  },
  dialog: {
    showMessageBox: vi.fn(async () => ({
      response: 1,
      checkboxChecked: false,
    })),
  },
  ipcMain: {
    handle: (channel: string, handler: Handler) =>
      handlers.set(channel, handler),
    on: () => undefined,
  },
}))

vi.mock("../analytics.js", () => ({
  hashWorkspaceRef: (value: string) => value,
  trackEvent: vi.fn(),
}))

const { registerIpcHandlers } = await import("../ipc.js")
const { app, dialog } = await import("electron")
const { trackEvent } = await import("../analytics.js")

function setup(timeoutMs = 5 * 60 * 1000) {
  const webContents = new EventEmitter() as EventEmitter & {
    send: ReturnType<typeof vi.fn>
    isDestroyed: () => boolean
    mainFrame: { url: string; isDestroyed: () => boolean }
  }
  webContents.send = vi.fn()
  webContents.isDestroyed = () => false
  webContents.mainFrame = {
    url: pathToFileURL(join(__dirname, "../../renderer/index.html")).href,
    isDestroyed: () => false,
  }
  const event = { sender: webContents, senderFrame: webContents.mainFrame }

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
    runRawStdin: vi.fn(async () => ""),
    setSessionPassphrase: vi.fn(),
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
  const requestId = () =>
    (
      webContents.send.mock.calls
        .filter(([channel]) => channel === "secret_unlock_required")
        .at(-1)?.[1] as { requestId: string } | undefined
    )?.requestId
  return { win, webContents, unlock, cli, event, requestId }
}

describe("secret unlock IPC lifecycle", () => {
  beforeEach(() => {
    handlers.clear()
    vi.clearAllMocks()
    vi.stubEnv("ELECTRON_RENDERER_URL", "")
    vi.mocked(app.getAppPath).mockReturnValue("/tmp")
    vi.mocked(dialog.showMessageBox).mockResolvedValue({
      response: 1,
      checkboxChecked: false,
    })
  })

  it("trusts the bootstrap sibling renderer when Electron's app path is the main entrypoint directory", async () => {
    vi.mocked(app.getAppPath).mockReturnValue(join(__dirname, ".."))
    const { cli, event } = setup()
    expect(await handlers.get("secret_protection_status")?.(event)).toEqual({
      sessionUnlocked: false,
    })
    expect(cli.run).toHaveBeenCalledExactlyOnceWith([
      "secret",
      "protection",
      "status",
    ])
    event.senderFrame.url = pathToFileURL(
      join(__dirname, "../dist/renderer/index.html"),
    ).href
    expect(await handlers.get("secret_protection_status")?.(event)).toEqual({
      ok: false,
      message: "Secret operations require the main application window.",
    })
    expect(cli.run).toHaveBeenCalledOnce()
  })

  it.each([{}, 123, true, [], undefined, null, "   "])(
    "rejects invalid protection passphrase %j without running the CLI",
    async (passphrase) => {
      const { cli, event } = setup()
      const result = await handlers.get("secret_protection_action")?.(event, {
        action: "change-passphrase",
        passphrase,
      })
      expect(result).toEqual({
        ok: false,
        message: "Enter a non-empty passphrase.",
      })
      expect(cli.runRawStdin).not.toHaveBeenCalled()
      expect(cli.setSessionPassphrase).not.toHaveBeenCalled()
      expect(dialog.showMessageBox).not.toHaveBeenCalled()
    },
  )

  it.each([
    "set-passphrase",
    "change-passphrase",
    "remove-passphrase",
    "remember",
    "forget",
  ])(
    "requires native confirmation before %s can use the session credential",
    async (action) => {
      const { cli, event, win } = setup()
      vi.mocked(dialog.showMessageBox).mockResolvedValue({
        response: 0,
        checkboxChecked: false,
      })
      const result = await handlers.get("secret_protection_action")?.(event, {
        action,
        passphrase: "private",
      })
      expect(result).toEqual({
        ok: false,
        message: "Secret protection change canceled.",
      })
      expect(dialog.showMessageBox).toHaveBeenCalledWith(
        win,
        expect.objectContaining({ defaultId: 0, cancelId: 0 }),
      )
      expect(cli.runRaw).not.toHaveBeenCalled()
      expect(cli.runRawStdin).not.toHaveBeenCalled()
      expect(cli.setSessionPassphrase).not.toHaveBeenCalled()
    },
  )

  it("changes protection only after native approval and preserves stdin-only passphrase transport", async () => {
    const { cli, event } = setup()
    expect(
      await handlers.get("secret_protection_action")?.(event, {
        action: "change-passphrase",
        passphrase: "private",
      }),
    ).toEqual({ ok: true })
    expect(cli.runRawStdin).toHaveBeenCalledWith(
      ["secret", "protection", "change-passphrase", "--stdin"],
      "private",
    )
    expect(cli.setSessionPassphrase).toHaveBeenCalledWith("private")
    expect(
      JSON.stringify(vi.mocked(dialog.showMessageBox).mock.calls),
    ).not.toContain("private")
  })

  it.each(["set-passphrase", "change-passphrase"])(
    "does not restore a cleared credential when an in-flight %s finishes",
    async (action) => {
      const { cli, event } = setup()
      let finishCli!: (value: string) => void
      cli.runRawStdin.mockImplementation(
        () =>
          new Promise((resolve) => {
            finishCli = resolve
          }),
      )
      const operation = handlers.get("secret_protection_action")?.(event, {
        action,
        passphrase: "private",
      })
      await vi.waitFor(() => expect(cli.runRawStdin).toHaveBeenCalledOnce())
      await handlers.get("secret_session_clear")?.(event)
      finishCli("")
      await expect(operation).resolves.toEqual({ ok: true })
      expect(cli.setSessionPassphrase).toHaveBeenCalledExactlyOnceWith(
        undefined,
      )
    },
  )

  it("captures cache authority before delayed native approval so a clear remains effective", async () => {
    const { cli, event } = setup()
    let approve!: (value: {
      response: number
      checkboxChecked: boolean
    }) => void
    vi.mocked(dialog.showMessageBox).mockImplementation(
      () =>
        new Promise((resolve) => {
          approve = resolve
        }),
    )
    const operation = handlers.get("secret_protection_action")?.(event, {
      action: "change-passphrase",
      passphrase: "private",
    })
    expect(cli.runRawStdin).not.toHaveBeenCalled()
    await handlers.get("secret_session_clear")?.(event)
    approve({ response: 1, checkboxChecked: false })
    await expect(operation).resolves.toEqual({
      ok: false,
      message: "Secret protection change canceled.",
    })
    expect(cli.runRawStdin).not.toHaveBeenCalled()
    expect(cli.setSessionPassphrase).toHaveBeenCalledExactlyOnceWith(undefined)
  })

  it("does not let an older removal clear a newly supplied session after an explicit clear", async () => {
    const { cli, event, unlock, requestId } = setup()
    let finishCli!: (value: string) => void
    cli.runRaw.mockImplementation(
      () =>
        new Promise((resolve) => {
          finishCli = resolve
        }),
    )
    const operation = handlers.get("secret_protection_action")?.(event, {
      action: "remove-passphrase",
    })
    await vi.waitFor(() => expect(cli.runRaw).toHaveBeenCalledOnce())
    await handlers.get("secret_session_clear")?.(event)
    const pending = unlock()
    await handlers.get("secret_unlock_submit")?.(event, {
      requestId: requestId(),
      passphrase: "new-private",
    })
    await expect(pending).resolves.toBe("new-private")
    // The real runner caches the result of its main-process unlock handler.
    cli.setSessionPassphrase("new-private")
    finishCli("")
    await expect(operation).resolves.toEqual({ ok: true })
    expect(cli.setSessionPassphrase.mock.calls).toEqual([
      [undefined],
      ["new-private"],
    ])
  })

  it.each(["change-passphrase", "remove-passphrase"])(
    "does not mutate the cache if the sender navigates during the %s CLI operation",
    async (action) => {
      const { cli, event, webContents } = setup()
      let finishCli!: (value: string) => void
      const run = action === "change-passphrase" ? cli.runRawStdin : cli.runRaw
      run.mockImplementation(
        () =>
          new Promise((resolve) => {
            finishCli = resolve
          }),
      )
      const operation = handlers.get("secret_protection_action")?.(event, {
        action,
        passphrase: "private",
      })
      await vi.waitFor(() => expect(run).toHaveBeenCalledOnce())
      webContents.mainFrame.url = "https://untrusted.example/"
      finishCli("")
      await expect(operation).resolves.toEqual({ ok: true })
      expect(cli.setSessionPassphrase).not.toHaveBeenCalled()
    },
  )

  it("rejects an untrusted sender on every secret channel before accessing the CLI or session", async () => {
    const { cli } = setup()
    for (const name of [
      "secret_protection_action",
      "secret_protection_status",
      "secret_session_clear",
      "secret_unlock_submit",
      "secret_set",
      "secret_delete",
      "secret_list",
      "secret_attach",
      "secret_detach",
      "context_delete",
      "workspace_up",
      "workspace_rebuild",
      "workspace_reset",
    ]) {
      expect(
        await handlers.get(name)?.(
          {},
          { action: "remove-passphrase", passphrase: "private" },
        ),
      ).toEqual({
        ok: false,
        message: "Secret operations require the main application window.",
      })
    }
    expect(dialog.showMessageBox).not.toHaveBeenCalled()
    expect(cli.run).not.toHaveBeenCalled()
    expect(cli.runRaw).not.toHaveBeenCalled()
    expect(cli.runRawStdin).not.toHaveBeenCalled()
    expect(cli.setSessionPassphrase).not.toHaveBeenCalled()
    expect(trackEvent).not.toHaveBeenCalled()
  })

  it("rechecks the trusted document after native approval before mutating protection", async () => {
    const { cli, event, webContents } = setup()
    vi.mocked(dialog.showMessageBox).mockImplementation(async () => {
      webContents.mainFrame.url = "https://untrusted.example/"
      return { response: 1, checkboxChecked: false }
    })
    expect(
      await handlers.get("secret_protection_action")?.(event, {
        action: "remove-passphrase",
      }),
    ).toEqual({
      ok: false,
      message: "Secret operations require the main application window.",
    })
    expect(cli.runRaw).not.toHaveBeenCalled()
    expect(cli.setSessionPassphrase).not.toHaveBeenCalled()
  })

  it("rejects remembering without a pending matching request before dialog or CLI access", async () => {
    const { cli, event } = setup()
    expect(
      await handlers.get("secret_unlock_submit")?.(event, {
        requestId: "absent",
        passphrase: "private",
        remember: true,
      }),
    ).toEqual({ ok: false, message: "Unlock request is no longer active." })
    expect(dialog.showMessageBox).not.toHaveBeenCalled()
    expect(cli.runRaw).not.toHaveBeenCalled()
    expect(cli.setSessionPassphrase).not.toHaveBeenCalled()
  })

  it.each([null, "true", 1, {}, []])(
    "rejects invalid remember preference %j",
    async (remember) => {
      const { cli, event, unlock, requestId } = setup()
      const pending = unlock()
      expect(
        await handlers.get("secret_unlock_submit")?.(event, {
          requestId: requestId(),
          passphrase: "private",
          remember,
        }),
      ).toEqual({ ok: false, message: "Invalid remember preference." })
      expect(dialog.showMessageBox).not.toHaveBeenCalled()
      expect(cli.runRaw).not.toHaveBeenCalled()
      await handlers.get("secret_session_clear")?.(event)
      await expect(pending).resolves.toBeUndefined()
    },
  )

  it("native cancellation neither remembers nor caches and leaves its request unsettled", async () => {
    const { cli, event, unlock, win, requestId } = setup()
    const pending = unlock()
    vi.mocked(dialog.showMessageBox).mockResolvedValue({
      response: 0,
      checkboxChecked: false,
    })
    expect(
      await handlers.get("secret_unlock_submit")?.(event, {
        requestId: requestId(),
        passphrase: "private",
        remember: true,
      }),
    ).toEqual({ ok: false, message: "Secret protection change canceled." })
    expect(dialog.showMessageBox).toHaveBeenCalledWith(
      win,
      expect.objectContaining({
        message: "Remember the secrets passphrase in your OS keychain?",
        defaultId: 0,
        cancelId: 0,
      }),
    )
    expect(
      JSON.stringify(vi.mocked(dialog.showMessageBox).mock.calls),
    ).not.toContain("private")
    expect(cli.runRaw).not.toHaveBeenCalled()
    expect(cli.setSessionPassphrase).not.toHaveBeenCalled()
    await handlers.get("secret_unlock_submit")?.(event, {
      requestId: requestId(),
    })
    await expect(pending).resolves.toBeUndefined()
  })

  it("requires native approval before remembering the captured credential and settling its request", async () => {
    const { cli, event, unlock, requestId } = setup()
    const pending = unlock()
    let approve!: (value: {
      response: number
      checkboxChecked: boolean
    }) => void
    vi.mocked(dialog.showMessageBox).mockImplementation(
      () =>
        new Promise((resolve) => {
          approve = resolve
        }),
    )
    const args = {
      requestId: requestId(),
      passphrase: "original-private",
      remember: true,
    }
    const submission = handlers.get("secret_unlock_submit")?.(event, args)
    expect(cli.runRaw).not.toHaveBeenCalled()
    args.passphrase = "changed-private"
    args.remember = false
    args.requestId = "changed-request"
    approve({ response: 1, checkboxChecked: false })
    await expect(submission).resolves.toEqual({ ok: true })
    expect(cli.runRaw).toHaveBeenCalledExactlyOnceWith(
      ["secret", "protection", "remember"],
      {
        env: { DEVSY_SECRETS_PASSPHRASE: "original-private" },
        unlockRetried: true,
      },
    )
    await expect(pending).resolves.toBe("original-private")
    expect(cli.setSessionPassphrase).not.toHaveBeenCalled()
  })

  it.each(["dialog", "cli"] as const)(
    "cannot settle a replacement request after navigation during %s",
    async (stage) => {
      const { cli, event, unlock, webContents, requestId } = setup()
      const first = unlock()
      const oldId = requestId()
      let finishDialog!: (value: {
        response: number
        checkboxChecked: boolean
      }) => void
      let finishCli!: (value: string) => void
      if (stage === "dialog")
        vi.mocked(dialog.showMessageBox).mockImplementation(
          () =>
            new Promise((resolve) => {
              finishDialog = resolve
            }),
        )
      else
        cli.runRaw.mockImplementation(
          () =>
            new Promise((resolve) => {
              finishCli = resolve
            }),
        )
      const submission = handlers.get("secret_unlock_submit")?.(event, {
        requestId: oldId,
        passphrase: "stale-private",
        remember: true,
      })
      if (stage === "cli")
        await vi.waitFor(() => expect(cli.runRaw).toHaveBeenCalledOnce())
      webContents.emit(
        "did-start-navigation",
        {},
        webContents.mainFrame.url,
        false,
        true,
      )
      await expect(first).resolves.toBeUndefined()
      let newSettled = false
      const second = unlock().then((value) => {
        newSettled = true
        return value
      })
      expect(requestId()).not.toBe(oldId)
      if (stage === "dialog")
        finishDialog({ response: 1, checkboxChecked: false })
      else finishCli("")
      await expect(submission).resolves.toEqual({
        ok: false,
        message: "Unlock request is no longer active.",
      })
      expect(newSettled).toBe(false)
      expect(cli.runRaw).toHaveBeenCalledTimes(stage === "dialog" ? 0 : 1)
      expect(cli.setSessionPassphrase).not.toHaveBeenCalled()
      await handlers.get("secret_unlock_submit")?.(event, {
        requestId: requestId(),
        passphrase: "new-private",
        remember: false,
      })
      await expect(second).resolves.toBe("new-private")
    },
  )

  it.each(["dialog", "cli"] as const)(
    "revalidates the trusted sender after %s before settling",
    async (stage) => {
      const { cli, event, unlock, webContents, requestId } = setup()
      const pending = unlock()
      if (stage === "dialog")
        vi.mocked(dialog.showMessageBox).mockImplementation(async () => {
          webContents.mainFrame.url = "https://untrusted.example/"
          return { response: 1, checkboxChecked: false }
        })
      else
        cli.runRaw.mockImplementation(async () => {
          webContents.mainFrame.url = "https://untrusted.example/"
          return ""
        })
      expect(
        await handlers.get("secret_unlock_submit")?.(event, {
          requestId: requestId(),
          passphrase: "private",
          remember: true,
        }),
      ).toEqual({
        ok: false,
        message: "Secret operations require the main application window.",
      })
      expect(cli.runRaw).toHaveBeenCalledTimes(stage === "dialog" ? 0 : 1)
      expect(cli.setSessionPassphrase).not.toHaveBeenCalled()
      webContents.emit("render-process-gone")
      await expect(pending).resolves.toBeUndefined()
    },
  )

  it("rejects a late renderer submission for an expired request even when a new prompt is active", async () => {
    const { cli, event, unlock, requestId } = setup()
    const first = unlock()
    const oldId = requestId()
    await handlers.get("secret_session_clear")?.(event)
    await expect(first).resolves.toBeUndefined()
    const second = unlock()
    for (const id of [oldId, undefined, 42]) {
      expect(
        await handlers.get("secret_unlock_submit")?.(event, {
          requestId: id,
          passphrase: "stale-private",
          remember: true,
        }),
      ).toEqual({ ok: false, message: "Unlock request is no longer active." })
    }
    expect(dialog.showMessageBox).not.toHaveBeenCalled()
    expect(cli.runRaw).not.toHaveBeenCalled()
    await handlers.get("secret_unlock_submit")?.(event, {
      requestId: requestId(),
      passphrase: "new-private",
    })
    await expect(second).resolves.toBe("new-private")
  })

  it("shows and focuses a hidden window, re-notifies joiners, and replays when the renderer is ready", async () => {
    const { win, webContents, unlock, event, requestId } = setup()
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
    await submitted?.(event, { requestId: requestId(), passphrase: "private" })
    await expect(first).resolves.toBe("private")
    await expect(second).resolves.toBe("private")
    expect(dialog.showMessageBox).not.toHaveBeenCalled()
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
