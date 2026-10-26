// @vitest-environment node
import { join } from "node:path"
import { pathToFileURL } from "node:url"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { registerIpcHandlers } from "../ipc.js"
import { ProviderJobs } from "../provider-jobs.js"
import { DaemonState } from "../state.js"

const handlers = new Map<string, (...args: unknown[]) => unknown>()
let trustedEvent: unknown
let foreignEvent: unknown

vi.mock("electron", () => ({
  app: {
    getPath: () => "/tmp",
    getAppPath: () => "/tmp",
    getVersion: () => "0.0.0",
  },
  dialog: {},
  ipcMain: {
    handle: (channel: string, fn: (...args: unknown[]) => unknown) => {
      handlers.set(channel, fn)
    },
    on: () => undefined,
  },
}))

vi.mock("../analytics.js", () => ({
  hashWorkspaceRef: (v: string) => v,
  trackEvent: () => undefined,
}))

const ICON = {
  revision: "rev-a",
  mimeType: "image/png",
  dataBase64: Buffer.from("png").toString("base64"),
}

function setup() {
  const cli = {
    setUnlockHandler: vi.fn(),
    run: vi.fn(async (_args: string[]) => ICON),
    runRaw: vi.fn(async () => ""),
    runStreaming: vi.fn(
      async (
        _args: string[],
        _onLine: (line: string, stream: string) => void,
        onExit: (code: number) => void,
      ) => onExit(0),
    ),
  }
  const state = new DaemonState()
  const mainFrame = {
    url: pathToFileURL(join(__dirname, "../../renderer/index.html")).href,
    isDestroyed: () => false,
  }
  const webContents = { send: vi.fn(), mainFrame, isDestroyed: () => false }
  const win = { webContents, isDestroyed: () => false }
  trustedEvent = { sender: webContents, senderFrame: mainFrame }
  foreignEvent = {
    sender: webContents,
    senderFrame: { ...mainFrame, url: "https://example.com/" },
  }
  registerIpcHandlers({
    cli,
    state,
    providerJobs: new ProviderJobs(),
    getMainWindow: () => win,
  } as never)
  return { cli, state }
}

function invoke(
  event: unknown,
  channel: string,
  args: Record<string, unknown>,
) {
  const handler = handlers.get(channel)
  if (!handler) throw new Error(`${channel} not registered`)
  return handler(event, args)
}

const iconArgs = {
  providerName: "harvester",
  context: "default",
  theme: "light",
  expectedRevision: "rev-a",
}

describe("provider_icon IPC", () => {
  beforeEach(() => {
    vi.stubEnv("ELECTRON_RENDERER_URL", "")
    handlers.clear()
    vi.clearAllMocks()
  })

  it("returns the data URL for a trusted sender", async () => {
    const { cli } = setup()

    await expect(
      invoke(trustedEvent, "provider_icon", iconArgs),
    ).resolves.toEqual({
      revision: "rev-a",
      dataUrl: `data:image/png;base64,${ICON.dataBase64}`,
    })
    expect(cli.run).toHaveBeenCalledTimes(1)
  })

  it("rejects a sender that is not the main application document", async () => {
    const { cli } = setup()

    await expect(
      invoke(foreignEvent, "provider_icon", iconArgs),
    ).rejects.toThrow(/main application window/)
    await expect(invoke(undefined, "provider_icon", iconArgs)).rejects.toThrow()
    expect(cli.run).not.toHaveBeenCalled()
  })

  it("falls back to the active context when the caller sends none", async () => {
    const { cli, state } = setup()
    state.updateContexts([{ name: "work" }], "work")

    await invoke(trustedEvent, "provider_icon", { ...iconArgs, context: "" })

    const args = cli.run.mock.calls[0][0]
    expect(args[args.indexOf("--context") + 1]).toBe("work")
  })

  it("drops cached images when the provider is deleted", async () => {
    const { cli } = setup()
    await invoke(trustedEvent, "provider_icon", iconArgs)
    await invoke(trustedEvent, "provider_icon", iconArgs)
    expect(cli.run).toHaveBeenCalledTimes(1)

    await invoke(trustedEvent, "provider_delete", { name: "harvester" })
    await invoke(trustedEvent, "provider_icon", iconArgs)

    expect(cli.run).toHaveBeenCalledTimes(2)
  })

  it("drops cached images when the provider is renamed", async () => {
    const { cli } = setup()
    await invoke(trustedEvent, "provider_icon", iconArgs)

    await invoke(trustedEvent, "provider_rename", {
      name: "harvester",
      newName: "harvester-2",
    })
    await invoke(trustedEvent, "provider_icon", iconArgs)

    expect(cli.run).toHaveBeenCalledTimes(2)
  })

  it("drops cached images for every provider on context switch", async () => {
    const { cli } = setup()
    await invoke(trustedEvent, "provider_icon", iconArgs)

    await invoke(trustedEvent, "context_use", { name: "work" })
    await invoke(trustedEvent, "provider_icon", iconArgs)

    expect(cli.run).toHaveBeenCalledTimes(2)
  })

  it("drops cached images after a provider update", async () => {
    const { cli } = setup()
    await invoke(trustedEvent, "provider_icon", iconArgs)

    await invoke(trustedEvent, "provider_update", { name: "harvester" })
    await invoke(trustedEvent, "provider_icon", iconArgs)

    expect(cli.run).toHaveBeenCalledTimes(2)
  })

  it("rejects cached bytes after polling observes an external source replacement", async () => {
    const { cli, state } = setup()
    state.updateProviders([{ name: "harvester", iconRevision: "rev-a" }])
    await invoke(trustedEvent, "provider_icon", iconArgs)

    state.updateProviders([{ name: "harvester", iconRevision: "rev-b" }])
    cli.run.mockResolvedValue({ ...ICON, revision: "rev-b" })

    await expect(
      invoke(trustedEvent, "provider_icon", iconArgs),
    ).resolves.toEqual({ revision: "rev-b", stale: true })
  })

  it("does not serve cached bytes after polling observes deletion", async () => {
    const { cli, state } = setup()
    state.updateProviders([{ name: "harvester", iconRevision: "rev-a" }])
    await invoke(trustedEvent, "provider_icon", iconArgs)

    state.updateProviders([])
    cli.run.mockRejectedValue(new Error("provider not found"))

    await expect(
      invoke(trustedEvent, "provider_icon", iconArgs),
    ).rejects.toThrow("provider not found")
  })

  it("does not retain an old context's bytes across externally observed switches", async () => {
    const { cli, state } = setup()
    state.updateContexts([{ name: "default" }, { name: "work" }], "default")
    await invoke(trustedEvent, "provider_icon", iconArgs)

    state.updateContexts([{ name: "default" }, { name: "work" }], "work")
    state.updateContexts([{ name: "default" }, { name: "work" }], "default")
    cli.run.mockRejectedValue(new Error("provider no longer installed"))

    await expect(
      invoke(trustedEvent, "provider_icon", iconArgs),
    ).rejects.toThrow("provider no longer installed")
  })

  it.each(["replacement", "deletion", "context"] as const)(
    "revokes pending bytes after an externally observed %s without refilling stale cache",
    async (transition) => {
      const { cli, state } = setup()
      state.updateProviders([{ name: "harvester", iconRevision: "rev-a" }])
      const { promise, resolve: finish } = Promise.withResolvers<typeof ICON>()
      cli.run.mockImplementationOnce(() => promise)
      const pending = invoke(trustedEvent, "provider_icon", iconArgs)

      if (transition === "replacement")
        state.updateProviders([{ name: "harvester", iconRevision: "rev-b" }])
      else if (transition === "deletion") state.updateProviders([])
      else state.updateContexts([{ name: "default" }, { name: "work" }], "work")
      finish(ICON)

      await expect(pending).resolves.toEqual({
        revision: transition === "replacement" ? "rev-b" : "",
        stale: true,
      })
      cli.run.mockRejectedValueOnce(new Error("new installed read failed"))
      await expect(
        invoke(trustedEvent, "provider_icon", iconArgs),
      ).rejects.toThrow("new installed read failed")
    },
  )
})
