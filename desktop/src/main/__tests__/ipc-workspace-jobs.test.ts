// @vitest-environment node
import { EventEmitter } from "node:events"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { ProviderJobs } from "../provider-jobs.js"
import { WorkspaceJobs } from "../workspace-jobs.js"
const handlers = new Map<string, (...args: any[]) => any>()
vi.mock("electron", () => ({
  app: { getPath: () => "/tmp", getVersion: () => "0.0.0" },
  dialog: {},
  ipcMain: {
    handle: (channel: string, fn: (...args: any[]) => any) =>
      handlers.set(channel, fn),
    on: () => undefined,
  },
}))
vi.mock("../analytics.js", () => ({
  hashWorkspaceRef: (value: string) => value,
  trackEvent: () => undefined,
}))
const { registerIpcHandlers } = await import("../ipc.js")
function setup() {
  const jobs = new WorkspaceJobs()
  let exit!: (code: number) => void
  let line!: (line: string, stream: "stdout") => void
  const cli = {
    run: vi.fn(async () => ({ id: "task-1" })),
    runRaw: vi.fn(
      async () =>
        '{"kind":"status","schemaVersion":1,"phase":"building_image","state":"started"}',
    ),
    runStreaming: vi.fn(async (_args, onLine, onExit) => {
      exit = onExit
      line = onLine
      return Object.assign(new EventEmitter(), {
        exitCode: null,
        signalCode: null,
        kill: vi.fn(),
      })
    }),
    cancelFor: vi.fn(async () => {}),
  }
  const send = vi.fn()
  const actions = registerIpcHandlers({
    cli,
    state: {
      workspaceContext: () => "ctx",
      providerList: () => [],
      workspaceList: () => [],
    },
    logStore: {
      createLogFile: () => "/tmp/log",
      appendLog: () => true,
      closeLog: async () => {},
      onDrain: async () => {},
    },
    pty: { cancelFor: vi.fn(async () => {}) },
    getMainWindow: () => ({ webContents: { send } }),
    providerJobs: new ProviderJobs(),
    workspaceJobs: jobs,
  } as any)
  return {
    jobs,
    cli,
    send,
    actions,
    exit: (code: number) => exit(code),
    line: (text: string) => line(text, "stdout"),
  }
}
function invoke(channel: string, args = { workspaceId: "ws" }) {
  return handlers.get(channel)!({}, args)
}
async function flush() {
  for (let i = 0; i < 20; i++) await Promise.resolve()
}
beforeEach(() => {
  handlers.clear()
  vi.clearAllMocks()
  vi.useFakeTimers()
})
afterEach(() => vi.useRealTimers())
describe("workspace lifecycle IPC", () => {
  it.each(["delete", "stop", "rebuild", "reset"])(
    "acknowledges %s while shutdown is blocked",
    async (action) => {
      const ctx = setup()
      let release!: () => void
      ctx.cli.cancelFor.mockImplementation(
        () =>
          new Promise<void>((resolve) => {
            release = resolve
          }),
      )
      const commandId = invoke(`workspace_${action}`)
      expect(typeof commandId).toBe("string")
      expect(ctx.jobs.get("ws")).toMatchObject({
        commandId,
        state: "running",
        phase: "Closing connections",
      })
      await flush()
      expect(ctx.cli.runStreaming).not.toHaveBeenCalled()
      release()
      await flush()
      ctx.exit(0)
      await flush()
      expect(ctx.jobs.get("ws")?.state).toBe("succeeded")
    },
  )
  it("records preparation failure without launching the CLI", async () => {
    const ctx = setup()
    ctx.cli.cancelFor.mockRejectedValue(new Error("shutdown failed"))
    invoke("workspace_delete")
    await flush()
    expect(ctx.jobs.get("ws")).toMatchObject({
      state: "failed",
      error: "shutdown failed",
    })
    expect(ctx.cli.runStreaming).not.toHaveBeenCalled()
  })
  it("rejects duplicate deletes", () => {
    setup()
    invoke("workspace_delete")
    expect(() => invoke("workspace_delete")).toThrow("already in progress")
  })
  it("reconciles CLI failures and keeps their error", async () => {
    const ctx = setup()
    const refresh = vi.fn(async () => {})
    ctx.jobs.setRefresh(refresh)
    invoke("workspace_delete")
    await flush()
    ctx.exit(1)
    await flush()
    expect(ctx.jobs.get("ws")).toMatchObject({
      state: "failed",
      error: "Command exited with code 1",
    })
    expect(refresh).toHaveBeenCalledOnce()
  })
  it("does not interpret a child phase success as completion", async () => {
    const ctx = setup()
    invoke("workspace_rebuild")
    await flush()
    ctx.line(
      '{"kind":"status","schemaVersion":1,"phase":"building_image","state":"succeeded"}',
    )
    expect(ctx.jobs.get("ws")?.state).toBe("running")
  })
  it("checks the detached task when its follower exits cleanly", async () => {
    const ctx = setup()
    await handlers.get("workspace_up")!({}, { source: "ws", commandId: "up" })
    await flush()
    ctx.exit(0)
    await flush()
    expect(ctx.cli.runRaw).toHaveBeenCalled()
    expect(ctx.jobs.get("ws")?.state).toBe("running")
    ctx.cli.runRaw.mockResolvedValue(
      '{"kind":"result","outcome":"success","containerId":"c","remoteUser":"u","remoteWorkspaceFolder":"/w"}',
    )
    await vi.advanceTimersByTimeAsync(3000)
    await flush()
    expect(ctx.jobs.get("ws")?.state).toBe("succeeded")
  })
  it("uses the same registry for tray stop", async () => {
    const ctx = setup()
    const completion = ctx.actions.workspaceActions.stop("ws")
    expect(ctx.jobs.get("ws")?.activity).toBe("stopping")
    await flush()
    ctx.exit(0)
    await completion
    expect(ctx.jobs.get("ws")?.state).toBe("succeeded")
  })
})
