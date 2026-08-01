// @vitest-environment node
import { EventEmitter } from "node:events"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { ProviderJobs } from "../provider-jobs.js"
import { WorkspaceJobs } from "../workspace-jobs.js"

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

function fakeChild() {
  const child = new EventEmitter() as EventEmitter & {
    exitCode: number | null
    signalCode: string | null
    kill: () => void
  }
  child.exitCode = null
  child.signalCode = null
  child.kill = () => undefined
  return child
}

function setup(exitCode = 0) {
  const workspaceJobs = new WorkspaceJobs()
  const cli = {
    run: vi.fn(async () => []),
    runRaw: vi.fn(async () => ""),
    runStreaming: vi.fn(
      async (
        _cliArgs: string[],
        _onLine: (line: string, stream: "stdout" | "stderr") => void,
        onExit: (code: number, cliError?: unknown) => void,
      ) => {
        const child = fakeChild()
        setTimeout(() => {
          onExit(exitCode, exitCode === 0 ? undefined : { message: "boom" })
        }, 0)
        return child
      },
    ),
    cancelFor: vi.fn(async () => undefined),
  }
  const deps = {
    cli,
    state: { workspaceContext: () => "ctx", providerList: () => [] },
    logStore: {
      createLogFile: () => "/tmp/log.txt",
      appendLog: () => true,
      closeLog: async () => undefined,
      onDrain: async () => undefined,
    },
    pty: { cancelFor: vi.fn(async () => undefined) },
    getMainWindow: () => null,
    providerJobs: new ProviderJobs(),
    workspaceJobs,
  }
  // biome-ignore lint/suspicious/noExplicitAny: partial test doubles
  registerIpcHandlers(deps as any)
  return { workspaceJobs }
}

function invoke(channel: string, args: Record<string, unknown>) {
  const handler = handlers.get(channel)
  if (!handler) throw new Error(`${channel} not registered`)
  return handler({}, args)
}

function waitForExitCallback(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 10))
}

describe("workspace delete job lifecycle over IPC", () => {
  beforeEach(() => {
    handlers.clear()
    vi.clearAllMocks()
  })

  it("shows a deleting job while the command runs, then clears it", async () => {
    const { workspaceJobs } = setup(0)

    // workspace_delete resolves as soon as the CLI command is launched, well
    // before it exits — the job must already be visible at that point.
    await invoke("workspace_delete", { workspaceId: "ws1" })
    expect(workspaceJobs.get("ws1")).toEqual({ activity: "deleting" })

    await waitForExitCallback()

    expect(workspaceJobs.get("ws1")).toBeUndefined()
  })

  it("retains the failure when the delete command fails", async () => {
    const { workspaceJobs } = setup(1)

    await invoke("workspace_delete", { workspaceId: "ws1" })
    await waitForExitCallback()

    expect(workspaceJobs.get("ws1")?.error).toBe("boom")
  })
})
