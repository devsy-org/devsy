// @vitest-environment node
import { EventEmitter } from "node:events"
import { join } from "node:path"
import { pathToFileURL } from "node:url"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { ProviderJobs } from "../provider-jobs.js"
import { WorkspaceJobs } from "../workspace-jobs.js"

type Handler = (...args: unknown[]) => unknown

const handlers = new Map<string, Handler>()
let trustedEvent: unknown
vi.mock("electron", () => ({
  app: {
    getPath: () => "/tmp",
    getAppPath: () => "/tmp",
    getVersion: () => "0.0.0",
  },
  dialog: {},
  ipcMain: {
    handle: (channel: string, fn: Handler) => handlers.set(channel, fn),
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
  let exit!: (code: number, error?: unknown) => void
  let line!: (line: string, stream: "stdout") => void
  const cli = {
    setUnlockHandler: vi.fn(),
    run: vi.fn(async (_args: string[]) => ({ id: "task-1" })),
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
  const mainFrame = {
    url: pathToFileURL(join(__dirname, "../../renderer/index.html")).href,
    isDestroyed: () => false,
  }
  const webContents = { send, mainFrame, isDestroyed: vi.fn(() => false) }
  const win = { webContents, isDestroyed: () => false }
  trustedEvent = { sender: webContents, senderFrame: mainFrame }
  const createLogFile = vi.fn(() => "/tmp/log")
  const closeLog = vi.fn(async () => {})
  const appendLog = vi.fn(() => true)
  const getMainWindow = vi.fn(() => win)
  const actions = registerIpcHandlers({
    cli,
    state: {
      workspaceContext: () => "ctx",
      providerList: () => [],
      workspaceList: () => [],
    },
    logStore: {
      createLogFile,
      appendLog,
      closeLog,
      onDrain: async () => {},
    },
    pty: { cancelFor: vi.fn(async () => {}) },
    getMainWindow,
    providerJobs: new ProviderJobs(),
    workspaceJobs: jobs,
  } as unknown as Parameters<typeof registerIpcHandlers>[0])
  return {
    jobs,
    cli,
    closeLog,
    appendLog,
    createLogFile,
    getMainWindow,
    webContents,
    send,
    actions,
    exit: (code: number, error?: unknown) => exit(code, error),
    line: (text: string) => line(text, "stdout"),
  }
}
function invoke(channel: string, args: unknown = { workspaceId: "ws" }) {
  const handler = handlers.get(channel)
  if (!handler) throw new Error(`No handler registered for ${channel}`)
  return handler(trustedEvent, args)
}
const RESULT_LINE =
  '{"kind":"result","outcome":"success","containerId":"c","remoteUser":"u","remoteWorkspaceFolder":"/w"}'
const errorLine = (code: string, message: string) =>
  JSON.stringify({ kind: "error", outcome: "error", code, message })
const DETACHED = new Set(["rebuild", "reset"])
async function flush() {
  for (let i = 0; i < 20; i++) await Promise.resolve()
}
beforeEach(() => {
  vi.stubEnv("ELECTRON_RENDERER_URL", "")
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
      // Rebuild and reset follow a detached task and end on its result.
      if (DETACHED.has(action)) {
        expect(ctx.cli.run).toHaveBeenCalledWith(
          expect.arrayContaining(["workspace", "up", "ws", "--detach"]),
        )
        ctx.line(RESULT_LINE)
      } else ctx.exit(0)
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
    await invoke("workspace_up", { source: "ws", commandId: "up" })
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
  it("finishes progress after an unexpected launch exception and permits retry", async () => {
    const ctx = setup()
    vi.spyOn(ctx.actions.tunnelProcesses, "set").mockImplementationOnce(() => {
      throw new Error("unexpected follower registration error")
    })
    invoke("workspace_up", { source: "ws", commandId: "first" })
    await flush()
    expect(ctx.jobs.get("ws")).toMatchObject({ state: "failed" })
    const terminal = ctx.send.mock.calls.filter(
      ([channel, value]) => channel === "command-progress" && value.done,
    )
    expect(terminal).toHaveLength(1)
    expect(terminal[0][1]).toMatchObject({ commandId: "first", success: false })
    expect(ctx.closeLog).toHaveBeenCalledOnce()
    invoke("workspace_up", { source: "ws", commandId: "retry" })
    await flush()
    expect(ctx.jobs.get("ws")).toMatchObject({
      commandId: "retry",
      state: "running",
    })
    expect(ctx.cli.run).toHaveBeenCalledWith([
      "workspace",
      "task",
      "cancel",
      "task-1",
    ])
  })

  it("finishes progress if creating the log fails before submission", async () => {
    const ctx = setup()
    ctx.createLogFile.mockImplementationOnce(() => {
      throw new Error("log directory unavailable")
    })
    invoke("workspace_up", { source: "ws", commandId: "up" })
    await flush()
    expect(ctx.cli.run).not.toHaveBeenCalled()
    expect(ctx.jobs.get("ws")).toMatchObject({
      state: "failed",
      error: "log directory unavailable",
    })
    const terminal = ctx.send.mock.calls.filter(
      ([channel, value]) => channel === "command-progress" && value.done,
    )
    expect(terminal).toHaveLength(1)
    expect(terminal[0][1]).toMatchObject({
      success: false,
      cliError: { message: "log directory unavailable" },
    })
  })

  it("preserves preflight error and hint in one terminal event", async () => {
    const ctx = setup()
    ctx.cli.run.mockRejectedValueOnce(
      Object.assign(new Error("invalid ID"), {
        cliError: {
          code: "invalid_id",
          message: "invalid ID",
          hint: "Use lowercase",
        },
      }),
    )
    invoke("workspace_up", { source: "ws", commandId: "up" })
    await flush()
    const terminal = ctx.send.mock.calls.filter(
      ([channel, value]) => channel === "command-progress" && value.done,
    )
    expect(terminal).toHaveLength(1)
    expect(terminal[0][1]).toMatchObject({
      success: false,
      cliError: {
        code: "invalid_id",
        message: "invalid ID",
        hint: "Use lowercase",
      },
    })
    expect(ctx.jobs.get("ws")).toMatchObject({
      state: "failed",
      error: "invalid ID",
    })
    expect(ctx.closeLog).toHaveBeenCalledOnce()
  })

  it("reports a persisted failed task after its follower disconnects", async () => {
    const ctx = setup()
    invoke("workspace_up", { source: "ws", commandId: "up" })
    await flush()
    ctx.cli.runRaw.mockRejectedValue(new Error("task failed"))
    ctx.cli.run.mockResolvedValue([
      {
        id: "task-1",
        status: "failed",
        error: "invalid ID",
        errorCode: "invalid_id",
      },
    ] as never)
    ctx.exit(1)
    await flush()
    expect(ctx.jobs.get("ws")).toMatchObject({
      state: "failed",
      error: "invalid ID",
    })
    const terminal = ctx.send.mock.calls.filter(
      ([channel, value]) => channel === "command-progress" && value.done,
    )
    expect(terminal).toHaveLength(1)
    expect(terminal[0][1]).toMatchObject({
      success: false,
      cliError: { message: "invalid ID" },
    })
    expect(ctx.closeLog).toHaveBeenCalledOnce()
    ctx.cli.run.mockResolvedValue({ id: "task-2" })
    invoke("workspace_up", { source: "ws", commandId: "retry" })
    await flush()
    expect(ctx.jobs.get("ws")).toMatchObject({
      commandId: "retry",
      state: "running",
    })
    expect(ctx.cli.run.mock.calls.at(-1)?.[0]).not.toContain("cancel")
  })

  it("ignores a stale persisted result after a new generation owns the workspace", async () => {
    const ctx = setup()
    invoke("workspace_up", { source: "ws", commandId: "old" })
    await flush()
    let resolve!: (value: string) => void
    ctx.cli.runRaw.mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolve = done
        }),
    )
    ctx.exit(1)
    await flush()
    ctx.jobs.clear("ws")
    ctx.jobs.start("ws", "creating", "new")
    resolve('{"kind":"result","outcome":"success"}')
    await flush()
    expect(ctx.jobs.get("ws")).toMatchObject({
      commandId: "new",
      state: "running",
    })
    expect(
      ctx.send.mock.calls.filter(
        ([channel, value]) => channel === "command-progress" && value.done,
      ),
    ).toHaveLength(0)
  })

  it.each(["success", "failure"])(
    "keeps %s visible and unconfirmed when log close fails",
    async (outcome) => {
      const ctx = setup()
      ctx.closeLog.mockRejectedValueOnce(new Error("disk unavailable"))
      invoke("workspace_up", { source: "ws", commandId: "up" })
      await flush()
      ctx.line(
        outcome === "success"
          ? '{"kind":"result","outcome":"success"}'
          : '{"kind":"error","outcome":"error","code":"invalid_id","message":"invalid ID","hint":"Use lowercase"}',
      )
      await flush()
      expect(ctx.jobs.get("ws")).toMatchObject({
        state: outcome === "success" ? "reconciling" : "failed",
        logError: "Workspace log could not be saved: disk unavailable",
        ...(outcome === "failure" ? { error: "invalid ID" } : {}),
      })
      const terminal = ctx.send.mock.calls.filter(
        ([channel, value]) => channel === "command-progress" && value.done,
      )
      expect(terminal).toHaveLength(1)
      expect(terminal[0][1].success).toBe(outcome === "success")
      if (outcome === "failure")
        expect(terminal[0][1].cliError).toMatchObject({
          message: "invalid ID",
          hint: "Use lowercase",
        })
      await ctx.jobs.retryRefresh("ws")
      expect(ctx.jobs.get("ws")?.state).toBe(
        outcome === "success" ? "reconciling" : "failed",
      )
      expect(ctx.jobs.get("ws")?.logError).toBe(
        "Workspace log could not be saved: disk unavailable",
      )
    },
  )

  it.each(["success", "failure"])(
    "preserves terminal %s when renderer delivery throws",
    async (outcome) => {
      const ctx = setup()
      vi.stubEnv("DEVSY_TEST_TOKEN", "private-value")
      const unhandled: unknown[] = []
      const onUnhandled = (error: unknown) => unhandled.push(error)
      process.on("unhandledRejection", onUnhandled)
      try {
        invoke("workspace_up", { source: "ws", commandId: "up" })
        await flush()
        ctx.send.mockImplementation((channel, progress) => {
          if (channel === "command-progress" && progress.done)
            throw new Error("renderer delivery failed")
        })
        ctx.line(
          outcome === "success"
            ? '{"kind":"result","outcome":"success"}'
            : '{"kind":"error","outcome":"error","code":"invalid_id","message":"invalid ID token=private-value","hint":"Use lowercase"}',
        )
        await flush()
        await vi.advanceTimersByTimeAsync(0)
        expect(unhandled).toEqual([])
        expect(ctx.jobs.get("ws")).toMatchObject({
          state: outcome === "success" ? "succeeded" : "failed",
          ...(outcome === "failure" ? { error: "invalid ID token=***" } : {}),
        })
        expect(ctx.closeLog).toHaveBeenCalledOnce()
        if (outcome === "failure") {
          const terminal = ctx.send.mock.calls.find(
            ([channel, progress]) =>
              channel === "command-progress" && progress.done,
          )
          expect(terminal?.[1].cliError).toMatchObject({
            message: "invalid ID token=***",
            hint: "Use lowercase",
          })
        }
        invoke("workspace_up", { source: "ws", commandId: "retry" })
        await flush()
        expect(ctx.jobs.get("ws")).toMatchObject({
          commandId: "retry",
          state: "running",
        })
        expect(ctx.cli.run).not.toHaveBeenCalledWith([
          "workspace",
          "task",
          "cancel",
          "task-1",
        ])
      } finally {
        process.removeListener("unhandledRejection", onUnhandled)
        vi.unstubAllEnvs()
      }
    },
  )

  it.each(["destroyed contents", "window lookup throws"])(
    "finishes the main job when %s prevents progress delivery",
    async (boundary) => {
      const ctx = setup()
      invoke("workspace_up", { source: "ws", commandId: "up" })
      await flush()
      if (boundary === "destroyed contents")
        ctx.webContents.isDestroyed.mockReturnValue(true)
      else
        ctx.getMainWindow.mockImplementation(() => {
          throw new Error("window unavailable")
        })
      ctx.line('{"kind":"result","outcome":"success"}')
      await flush()
      expect(ctx.jobs.get("ws")?.state).toBe("succeeded")
      expect(
        ctx.send.mock.calls.filter(
          ([channel]) => channel === "command-progress",
        ),
      ).toHaveLength(0)
      expect(ctx.closeLog).toHaveBeenCalledOnce()
    },
  )

  it.each(["success", "failure"])(
    "keeps %s and task ownership after status delivery throws",
    async (outcome) => {
      const ctx = setup()
      invoke("workspace_up", { source: "ws", commandId: "up" })
      await flush()
      ctx.send.mockImplementation((channel) => {
        if (channel === "workspace-status")
          throw new Error("status renderer unavailable")
      })
      expect(() =>
        ctx.line(
          '{"kind":"status","schemaVersion":1,"phase":"building_image","state":"started"}',
        ),
      ).not.toThrow()
      expect(ctx.jobs.get("ws")).toMatchObject({ state: "running" })
      ctx.line(
        outcome === "success"
          ? '{"kind":"result","outcome":"success"}'
          : '{"kind":"error","outcome":"error","code":"invalid_id","message":"invalid ID","hint":"Use lowercase"}',
      )
      await flush()
      expect(ctx.jobs.get("ws")).toMatchObject({
        state: outcome === "failure" ? "failed" : "succeeded",
        ...(outcome === "failure" ? { error: "invalid ID" } : {}),
      })
      expect(ctx.closeLog).toHaveBeenCalledOnce()
      const terminal = ctx.send.mock.calls.filter(
        ([channel, progress]) =>
          channel === "command-progress" && progress.done,
      )
      expect(terminal).toHaveLength(1)
      if (outcome === "failure")
        expect(terminal[0][1].cliError).toMatchObject({
          code: "invalid_id",
          message: "invalid ID",
          hint: "Use lowercase",
        })
      invoke("workspace_up", { source: "ws", commandId: "retry" })
      await flush()
      expect(ctx.jobs.get("ws")).toMatchObject({
        commandId: "retry",
        state: "running",
      })
      expect(ctx.cli.run).not.toHaveBeenCalledWith([
        "workspace",
        "task",
        "cancel",
        "task-1",
      ])
    },
  )

  it.each([0, 1])(
    "retains action outcome %s when forwarded status delivery throws",
    async (exitCode) => {
      const ctx = setup()
      invoke("workspace_rebuild")
      await flush()
      ctx.send.mockImplementation((channel) => {
        if (channel === "workspace-status")
          throw new Error("status renderer unavailable")
      })
      expect(() =>
        ctx.line(
          '{"kind":"status","schemaVersion":1,"phase":"building_image","state":"started"}',
        ),
      ).not.toThrow()
      expect(ctx.jobs.get("ws")?.state).toBe("running")
      ctx.line(
        exitCode === 0
          ? RESULT_LINE
          : errorLine("action_failed", "Command exited with code 1"),
      )
      await flush()
      expect(ctx.jobs.get("ws")).toMatchObject({
        state: exitCode === 0 ? "succeeded" : "failed",
        ...(exitCode === 1 ? { error: "Command exited with code 1" } : {}),
      })
    },
  )

  it.each([0, 1])(
    "keeps action outcome %s unconfirmed when log close fails",
    async (exitCode) => {
      const ctx = setup()
      vi.stubEnv("TEST_API_TOKEN", "private-token")
      ctx.closeLog.mockRejectedValueOnce(new Error("disk unavailable"))
      invoke("workspace_rebuild")
      await flush()
      ctx.line(
        exitCode === 0
          ? RESULT_LINE
          : errorLine("action_failed", "Cannot rebuild private-token"),
      )
      await flush()
      expect(ctx.jobs.get("ws")).toMatchObject({
        state: exitCode === 0 ? "reconciling" : "failed",
        logError: "Workspace log could not be saved: disk unavailable",
        error: exitCode === 0 ? undefined : "Cannot rebuild ***",
      })
      const terminal = () =>
        ctx.send.mock.calls.filter(
          ([channel, value]) => channel === "command-progress" && value.done,
        )
      expect(terminal()).toHaveLength(1)
      expect(terminal()[0][1].success).toBe(exitCode === 0)
      if (exitCode !== 0)
        expect(terminal()[0][1].cliError).toMatchObject({
          code: "action_failed",
          message: "Cannot rebuild ***",
        })
      await ctx.jobs.retryRefresh("ws")
      expect(ctx.jobs.get("ws")).toMatchObject({
        state: exitCode === 0 ? "reconciling" : "failed",
        logError: "Workspace log could not be saved: disk unavailable",
        refreshError: undefined,
        error: exitCode === 0 ? undefined : "Cannot rebuild ***",
      })
      expect(terminal()).toHaveLength(1)
      expect(ctx.closeLog).toHaveBeenCalledOnce()
    },
  )

  it("uses the same registry for tray stop", async () => {
    const ctx = setup()
    const completion = ctx.actions.workspaceActions.stop("ws")
    expect(ctx.jobs.get("ws")?.activity).toBe("stopping")
    await flush()
    ctx.exit(0)
    await completion
    expect(ctx.jobs.get("ws")?.state).toBe("succeeded")
  })

  describe("run identity", () => {
    const RUN = "11111111-1111-4111-8111-111111111111"
    const INV = "a1111111-0000-4000-8000-000000000001"

    it.each([
      ["rebuild", "--recreate"],
      ["reset", "--reset"],
    ])(
      "submits %s as a detached up and keeps the run ID",
      async (action, flag) => {
        const ctx = setup()
        ctx.cli.run.mockResolvedValue({
          kind: "task",
          id: "task-9",
          runId: RUN,
        })
        invoke(`workspace_${action}`)
        await flush()
        expect(ctx.cli.run).toHaveBeenCalledWith([
          "workspace",
          "up",
          "ws",
          flag,
          "--detach",
        ])
        expect(ctx.cli.runStreaming.mock.calls[0][0]).toEqual([
          "workspace",
          "task",
          "logs",
          "task-9",
          "--follow",
        ])
        expect(ctx.jobs.get("ws")?.runId).toBe(RUN)
        ctx.line(RESULT_LINE)
        await flush()
        expect(ctx.jobs.get("ws")).toMatchObject({
          state: "succeeded",
          runId: RUN,
        })
      },
    )

    it("takes the run ID from a result envelope when submission lacks one", async () => {
      const ctx = setup()
      invoke("workspace_rebuild")
      await flush()
      expect(ctx.jobs.get("ws")?.runId).toBeUndefined()
      ctx.line(RESULT_LINE.replace("}", `,"runId":"${RUN}"}`))
      await flush()
      expect(ctx.jobs.get("ws")?.runId).toBe(RUN)
    })

    it("takes run and failed invocation from error context", async () => {
      const ctx = setup()
      invoke("workspace_reset")
      await flush()
      ctx.line(
        JSON.stringify({
          kind: "error",
          outcome: "error",
          code: "hook_failed",
          message: "postCreateCommand failed",
          context: { runId: RUN, failedInvocationId: INV },
        }),
      )
      await flush()
      expect(ctx.jobs.get("ws")).toMatchObject({
        state: "failed",
        runId: RUN,
        failedInvocationId: INV,
      })
    })

    it("keeps an old-envelope failure usable without run identity", async () => {
      const ctx = setup()
      invoke("workspace_rebuild")
      await flush()
      ctx.line(errorLine("x", "plain failure"))
      await flush()
      const job = ctx.jobs.get("ws")
      expect(job).toMatchObject({ state: "failed", error: "plain failure" })
      expect(job?.runId).toBeUndefined()
    })

    it("ignores identity that is not a canonical UUID", async () => {
      const ctx = setup()
      ctx.cli.run.mockResolvedValue({ kind: "task", id: "t", runId: "op-1" })
      invoke("workspace_rebuild")
      await flush()
      expect(ctx.jobs.get("ws")?.runId).toBeUndefined()
    })
  })

  describe("history reader", () => {
    it("cancels only readers and leaves the task follower running", async () => {
      const ctx = setup()
      invoke("workspace_rebuild")
      await flush()
      const follower = ctx.actions.tunnelProcesses.get("ws")
      expect(follower).toBeDefined()
      ctx.cli.cancelFor.mockClear()
      await invoke("workspace_log_run_cancel")
      expect(ctx.cli.cancelFor).not.toHaveBeenCalled()
      expect(ctx.actions.tunnelProcesses.get("ws")).toBe(follower)
      expect(ctx.jobs.get("ws")?.state).toBe("running")
    })

    it("returns null for a superseded read instead of an error", async () => {
      const ctx = setup()
      ctx.cli.run.mockImplementation(() => new Promise(() => undefined))
      const request = {
        context: "default",
        runId: "11111111-1111-4111-8111-111111111111",
      }
      const first = invoke(
        "workspace_log_run_read",
        request,
      ) as Promise<unknown>
      await invoke("workspace_log_run_cancel")
      await expect(first).resolves.toBeNull()
    })
  })
})
