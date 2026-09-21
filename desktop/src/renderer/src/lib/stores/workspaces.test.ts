import { get } from "svelte/store"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import {
  mockInvoke,
  mockListen,
  resetTauriMocks,
} from "$lib/__mocks__/tauri.js"
import { toasts } from "./toasts.js"
import {
  destroyWorkspaces,
  initWorkspaces,
  workspaceJobs,
  workspaces,
  workspacesLoading,
} from "./workspaces.js"

vi.mock("./toasts.js", () => ({
  toasts: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
  notificationHistory: {
    subscribe: () => () => {},
    remove: vi.fn(),
    clear: vi.fn(),
    unreadCount: { subscribe: () => () => {} },
  },
}))

const job = {
  activity: "deleting",
  commandId: "delete",
  state: "running",
  phase: "Closing connections",
}
beforeEach(() => {
  resetTauriMocks()
  vi.clearAllMocks()
  workspaces.set([])
  workspaceJobs.set({})
})
afterEach(() => destroyWorkspaces())
function event(payload: unknown) {
  const call = mockListen.mock.calls.find(
    (call) => call[0] === "workspaces-changed",
  )!
  call[1]({ payload })
}
describe("workspace snapshot store", () => {
  it("hydrates jobs and status after a reload without renderer polling", async () => {
    mockInvoke.mockResolvedValue({
      revision: 1,
      workspaces: [{ id: "ws", status: "Running" }],
      jobs: { ws: job },
    })
    await initWorkspaces()
    expect(get(workspaces)[0].status).toBe("Running")
    expect(get(workspaceJobs).ws).toEqual(job)
    expect(mockInvoke).toHaveBeenCalledWith("workspace_snapshot")
    expect(
      mockInvoke.mock.calls.some((call) => call[0] === "workspace_status"),
    ).toBe(false)
    expect(get(workspacesLoading)).toBe(false)
  })
  it("subscribes before loading and ignores a snapshot older than an event", async () => {
    mockInvoke.mockImplementation(async () => {
      event({
        revision: 3,
        workspaces: [{ id: "ws", status: "Stopped" }],
        jobs: { ws: job },
      })
      return {
        revision: 2,
        workspaces: [{ id: "ws", status: "Running" }],
        jobs: {},
      }
    })
    await initWorkspaces()
    expect(get(workspaces)[0].status).toBe("Stopped")
    expect(get(workspaceJobs).ws).toEqual(job)
    event({ revision: 1, workspaces: [], jobs: {} })
    expect(get(workspaces)).toHaveLength(1)
  })
  it("disposes listeners that resolve after destruction", async () => {
    let resolve!: (unlisten: () => void) => void
    const stop = vi.fn()
    mockListen.mockImplementation(
      () =>
        new Promise((done) => {
          resolve = done
        }),
    )
    const init = initWorkspaces()
    destroyWorkspaces()
    resolve(stop)
    await init
    expect(stop).toHaveBeenCalledOnce()
    expect(mockInvoke).not.toHaveBeenCalled()
  })
  it("ignores an old initialization after a new one", async () => {
    let release!: (value: unknown) => void
    mockInvoke.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          release = resolve
        }),
    )
    const first = initWorkspaces()
    await Promise.resolve()
    await Promise.resolve()
    mockInvoke.mockResolvedValue({
      revision: 4,
      workspaces: [{ id: "new" }],
      jobs: {},
    })
    await initWorkspaces()
    release({ revision: 100, workspaces: [{ id: "old" }], jobs: {} })
    await first
    expect(get(workspaces)[0].id).toBe("new")
  })
  it("stays silent while a job runs or reconciles, then confirms once", async () => {
    mockInvoke.mockResolvedValue({
      revision: 1,
      workspaces: [{ id: "ws", status: "Running" }],
      jobs: { ws: job },
    })
    await initWorkspaces()
    event({
      revision: 2,
      workspaces: [{ id: "ws", status: "Running" }],
      jobs: { ws: { ...job, state: "reconciling", phase: "Refreshing list" } },
    })
    expect(toasts.success).not.toHaveBeenCalled()
    expect(toasts.error).not.toHaveBeenCalled()
    event({
      revision: 3,
      workspaces: [],
      jobs: { ws: { ...job, state: "succeeded" } },
    })
    expect(toasts.success).toHaveBeenCalledTimes(1)
    expect(toasts.success).toHaveBeenCalledWith("ws: Workspace deleted")
  })
  it("reports an operation failure once, sticky, with the operation named", async () => {
    mockInvoke.mockResolvedValue({
      revision: 1,
      workspaces: [{ id: "ws", status: "Running" }],
      jobs: { ws: { ...job, activity: "stopping", commandId: "stop" } },
    })
    await initWorkspaces()
    event({
      revision: 2,
      workspaces: [{ id: "ws", status: "Running" }],
      jobs: {
        ws: {
          ...job,
          activity: "stopping",
          commandId: "stop",
          state: "failed",
          error: "provider unavailable",
        },
      },
    })
    expect(toasts.error).toHaveBeenCalledTimes(1)
    expect(toasts.error).toHaveBeenCalledWith(
      "ws: Stop failed - provider unavailable",
      {
        sticky: true,
        action: { label: "View logs", onClick: expect.any(Function) },
      },
    )
  })
  it("toasts when a terminal job is the first observation of its command", async () => {
    mockInvoke.mockResolvedValue({
      revision: 1,
      workspaces: [{ id: "ws", status: "Running" }],
      jobs: {},
    })
    await initWorkspaces()
    event({
      revision: 2,
      workspaces: [{ id: "ws", status: "Stopped" }],
      jobs: {
        ws: {
          ...job,
          activity: "stopping",
          commandId: "stop",
          state: "succeeded",
        },
      },
    })
    expect(toasts.success).toHaveBeenCalledTimes(1)
    expect(toasts.success).toHaveBeenCalledWith("ws: Workspace stopped")
  })
  it("does not toast while a refresh is stalled", async () => {
    mockInvoke.mockResolvedValue({
      revision: 1,
      workspaces: [{ id: "ws", status: "Running" }],
      jobs: { ws: job },
    })
    await initWorkspaces()
    event({
      revision: 2,
      workspaces: [{ id: "ws", status: "Running" }],
      jobs: { ws: { ...job, state: "reconciling", refreshError: "offline" } },
    })
    expect(toasts.success).not.toHaveBeenCalled()
    expect(toasts.error).not.toHaveBeenCalled()
  })
  it("leaves existing observations intact when snapshot loading fails", async () => {
    workspaces.set([{ id: "ws", status: "Running" }])
    mockInvoke.mockRejectedValue(new Error("offline"))
    await initWorkspaces()
    expect(get(workspaces)[0].status).toBe("Running")
    expect(get(workspacesLoading)).toBe(false)
  })
})
