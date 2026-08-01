// @vitest-environment node
import { EventEmitter } from "node:events"
import { beforeEach, describe, expect, it, vi } from "vitest"

const handlers = new Map<string, (...args: unknown[]) => unknown>()

vi.mock("electron", () => ({
  app: { getPath: () => "/tmp", getVersion: () => "0.0.0" },
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

const { registerIpcHandlers } = await import("../ipc.js")

function invokeUp(workspaceId: string): Promise<string> {
  const handler = handlers.get("workspace_up")
  if (!handler) throw new Error("workspace_up not registered")
  return handler({}, { source: workspaceId, workspaceId }) as Promise<string>
}

function invokeStop(workspaceId: string): Promise<string> {
  const handler = handlers.get("workspace_stop")
  if (!handler) throw new Error("workspace_stop not registered")
  return handler({}, { workspaceId }) as Promise<string>
}

/** A child that reports itself still alive, so cancel must await its exit. */
function fakeChild() {
  const child = new EventEmitter() as EventEmitter & {
    exitCode: number | null
    signalCode: string | null
    kill: (signal?: string) => void
  }
  child.exitCode = null
  child.signalCode = null
  child.kill = () => {
    setTimeout(() => {
      child.exitCode = 0
      child.emit("close")
    }, 0)
  }
  return child
}

function setup(
  overrides: {
    run?: (args: string[]) => Promise<unknown>
    failStreaming?: Error
  } = {},
) {
  const calls: string[][] = []
  const cli = {
    run: vi.fn(async (args: string[]) => {
      calls.push(args)
      if (overrides.run) return overrides.run(args)
      if (args.includes("--detach")) return { kind: "task", id: "task-1" }
      return {}
    }),
    runStreaming: vi.fn(
      async (
        _args: string[],
        onLine: (line: string, stream: "stdout" | "stderr") => void,
        onExit: (code: number, cliError?: unknown) => void,
      ) => {
        if (overrides.failStreaming) throw overrides.failStreaming
        stream = { onLine, onExit }
        return fakeChild()
      },
    ),
    cancelFor: vi.fn(async () => undefined),
  }
  let stream: {
    onLine: (line: string, s: "stdout" | "stderr") => void
    onExit: (code: number, cliError?: unknown) => void
  } | null = null
  const sent: Array<{ channel: string; payload: Record<string, unknown> }> = []
  const win = {
    webContents: {
      send: (channel: string, payload: Record<string, unknown>) => {
        sent.push({ channel, payload })
      },
    },
    isDestroyed: () => false,
  }
  const deps = {
    cli,
    state: {
      workspaceContext: () => "ctx",
      providerList: () => [],
    },
    logStore: {
      createLogFile: () => "/tmp/log.txt",
      appendLog: () => true,
      closeLog: async () => undefined,
      onDrain: async () => undefined,
    },
    pty: { cancelFor: vi.fn(async () => undefined) },
    getMainWindow: () => win,
  }
  // biome-ignore lint/suspicious/noExplicitAny: partial test doubles
  const api = registerIpcHandlers(deps as any)
  return { cli, calls, api, sent, stream: () => stream }
}

function statusEnvelope(phase: string, step?: string) {
  return JSON.stringify({
    kind: "status",
    pipeline: "workspace_up",
    phase,
    ...(step ? { step } : {}),
    started: true,
  })
}

describe("workspace_up detached task tracking", () => {
  beforeEach(() => {
    handlers.clear()
    vi.clearAllMocks()
  })

  it("cancels the prior task before submitting a replacement", async () => {
    const { calls } = setup()

    await invokeUp("ws-1")
    await invokeUp("ws-1")

    const cancels = calls.filter((a) => a.includes("cancel"))
    expect(cancels).toEqual([["workspace", "task", "cancel", "task-1"]])
  })

  it("serializes concurrent submissions so neither task is left orphaned", async () => {
    let seq = 0
    const { calls } = setup({
      run: async (args) => {
        if (args.includes("--detach")) {
          // Yield so an unserialized handler would interleave here.
          await new Promise((r) => setTimeout(r, 5))
          seq += 1
          return { kind: "task", id: `task-${seq}` }
        }
        return {}
      },
    })

    await Promise.all([invokeUp("ws-1"), invokeUp("ws-1")])

    const cancels = calls.filter((a) => a.includes("cancel"))
    expect(cancels).toEqual([["workspace", "task", "cancel", "task-1"]])
  })

  it("keeps the task cancellable when cancellation fails", async () => {
    let failCancel = true
    const { calls } = setup({
      run: async (args) => {
        if (args.includes("cancel")) {
          if (failCancel) throw new Error("cancel boom")
          return {}
        }
        if (args.includes("--detach")) return { kind: "task", id: "task-1" }
        return {}
      },
    })

    await invokeUp("ws-1")

    await invokeUp("ws-1")
    expect(calls.filter((a) => a.includes("--detach"))).toHaveLength(1)

    failCancel = false
    await invokeStop("ws-1")
    const cancels = calls.filter((a) => a.includes("cancel"))
    expect(cancels).toEqual([
      ["workspace", "task", "cancel", "task-1"],
      ["workspace", "task", "cancel", "task-1"],
    ])
  })

  it("forwards status envelopes as workspace-status events", async () => {
    const { sent, stream } = setup()
    await invokeUp("ws-1")

    stream()?.onLine(statusEnvelope("building_image"), "stdout")

    const statuses = sent.filter((s) => s.channel === "workspace-status")
    expect(statuses).toHaveLength(1)
    expect(statuses[0].payload).toMatchObject({
      workspaceId: "ws-1",
      phase: "building_image",
      started: true,
    })
  })

  it("does not treat stderr lines as envelopes", async () => {
    const { sent, stream } = setup()
    await invokeUp("ws-1")

    stream()?.onLine(statusEnvelope("building_image"), "stderr")

    expect(sent.filter((s) => s.channel === "workspace-status")).toHaveLength(0)
  })

  it("releases the task on a result envelope, before the exit callback", async () => {
    const { calls, stream } = setup()
    await invokeUp("ws-1")

    stream()?.onLine(
      JSON.stringify({ kind: "result", outcome: "success" }),
      "stdout",
    )
    stream()?.onExit(0)
    await invokeUp("ws-1")

    expect(calls.filter((a) => a.includes("cancel"))).toEqual([])
  })

  it("releases the task on an error envelope", async () => {
    const { calls, stream } = setup()
    await invokeUp("ws-1")

    stream()?.onLine(
      JSON.stringify({ kind: "error", outcome: "error", message: "boom" }),
      "stdout",
    )
    stream()?.onExit(1)
    await invokeUp("ws-1")

    expect(calls.filter((a) => a.includes("cancel"))).toEqual([])
  })

  it("keeps the task cancellable when the follower fails to start", async () => {
    // The task is already submitted at this point, so losing the id here would
    // orphan a running workspace up with nothing left to cancel it by.
    const { calls } = setup({ failStreaming: new Error("spawn ENOENT") })

    await expect(invokeUp("ws-1")).resolves.toBeTruthy()

    await invokeStop("ws-1")
    expect(calls.filter((a) => a.includes("cancel"))).toEqual([
      ["workspace", "task", "cancel", "task-1"],
    ])
  })

  it("keeps the task registered when the follower exits without an envelope", async () => {
    const { calls, stream } = setup()
    await invokeUp("ws-1")

    stream()?.onExit(1, { code: "boom", message: "follower died" })
    await invokeUp("ws-1")

    expect(calls.filter((a) => a.includes("cancel"))).toEqual([
      ["workspace", "task", "cancel", "task-1"],
    ])
  })
})
