import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import {
  mockInvoke,
  mockListen,
  resetTauriMocks,
} from "$lib/__mocks__/tauri.js"
import { goto } from "$lib/router.js"
import { toasts } from "./toasts.js"
import { destroyWorkspaces, initWorkspaces } from "./workspaces.js"

vi.mock("$lib/router.js", () => ({ goto: vi.fn() }))
vi.mock("./toasts.js", () => ({
  toasts: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}))

const RUN = "11111111-1111-4111-8111-111111111111"
const INV = "a1111111-0000-4000-8000-000000000001"

beforeEach(() => {
  resetTauriMocks()
  vi.clearAllMocks()
})
afterEach(() => destroyWorkspaces())

async function failureAction(job: Record<string, unknown>) {
  mockInvoke.mockResolvedValue({
    revision: 1,
    workspaces: [{ id: "ws", status: "Running", context: "work" }],
    jobs: {},
  })
  await initWorkspaces()
  const callback = mockListen.mock.calls[0][1] as (e: {
    payload: unknown
  }) => void
  callback({
    payload: {
      revision: 2,
      workspaces: [{ id: "ws", status: "Running", context: "work" }],
      jobs: {
        ws: {
          activity: "creating",
          commandId: "c",
          state: "failed",
          phase: "",
          error: "hook failed",
          ...job,
        },
      },
    },
  })
  const [, options] = vi.mocked(toasts.error).mock.calls[0]
  options?.action?.onClick()
}

describe("failure notification", () => {
  it("pins View logs to the failed run and invocation", async () => {
    await failureAction({ runId: RUN, failedInvocationId: INV })
    const route = vi.mocked(goto).mock.calls[0][0] as string
    const url = new URL(route, "http://x")
    expect(url.pathname).toBe("/workspaces/ws")
    expect(url.searchParams.get("run")).toBe(RUN)
    expect(url.searchParams.get("invocation")).toBe(INV)
    expect(url.searchParams.get("context")).toBe("work")
  })

  it("keeps the plain logs route for old envelopes without a run", async () => {
    await failureAction({})
    expect(goto).toHaveBeenCalledWith("/workspaces/ws?tab=logs")
  })
})
