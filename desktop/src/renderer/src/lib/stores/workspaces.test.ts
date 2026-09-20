import { get } from "svelte/store"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import {
  mockInvoke,
  mockListen,
  resetTauriMocks,
} from "$lib/__mocks__/tauri.js"
import {
  destroyWorkspaces,
  initWorkspaces,
  workspaces,
  workspaceJobs,
  workspacesLoading,
} from "./workspaces.js"

const job = {
  activity: "deleting",
  commandId: "delete",
  state: "running",
  phase: "Closing connections",
}
beforeEach(() => {
  resetTauriMocks()
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
  it("leaves existing observations intact when snapshot loading fails", async () => {
    workspaces.set([{ id: "ws", status: "Running" }])
    mockInvoke.mockRejectedValue(new Error("offline"))
    await initWorkspaces()
    expect(get(workspaces)[0].status).toBe("Running")
    expect(get(workspacesLoading)).toBe(false)
  })
})
