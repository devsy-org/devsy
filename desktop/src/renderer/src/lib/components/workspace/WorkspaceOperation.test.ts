import { cleanup, fireEvent, render, waitFor } from "@testing-library/svelte"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { mockInvoke, resetTauriMocks } from "$lib/__mocks__/tauri.js"
import { workspaceJobs } from "$lib/stores/workspaces.js"
import { toasts } from "$lib/stores/toasts.js"
import WorkspaceOperation from "./WorkspaceOperation.svelte"
vi.mock("$lib/stores/toasts.js", () => ({
  toasts: { success: vi.fn(), error: vi.fn() },
}))
afterEach(cleanup)
beforeEach(() => {
  resetTauriMocks()
  workspaceJobs.set({})
  vi.clearAllMocks()
})
describe("WorkspaceOperation", () => {
  it("keeps the action visible over a stale runtime observation", () => {
    workspaceJobs.set({
      ws: {
        commandId: "delete",
        activity: "deleting",
        state: "running",
        phase: "Closing connections",
      },
    })
    const ui = render(WorkspaceOperation, { id: "ws", status: "Running" })
    expect(ui.getByText("Deleting")).toBeTruthy()
    expect(ui.getByText("Closing connections")).toBeTruthy()
    expect(ui.queryByText("Running")).toBeNull()
  })
  it("retries observation only and reports an IPC rejection", async () => {
    workspaceJobs.set({
      ws: {
        commandId: "delete",
        activity: "deleting",
        state: "reconciling",
        phase: "Refreshing list",
        refreshError: "offline",
      },
    })
    mockInvoke.mockRejectedValue(new Error("IPC unavailable"))
    const ui = render(WorkspaceOperation, { id: "ws", status: "Running" })
    expect(ui.getByText("Deleted")).toBeTruthy()
    await fireEvent.click(ui.getByRole("button", { name: "Retry refresh" }))
    await waitFor(() =>
      expect(toasts.error).toHaveBeenCalledWith(
        expect.stringContaining("IPC unavailable"),
      ),
    )
    expect(mockInvoke).toHaveBeenCalledWith("workspace_refresh", {
      workspaceId: "ws",
    })
    expect(
      mockInvoke.mock.calls.some((call) => call[0] === "workspace_delete"),
    ).toBe(false)
    expect(ui.getByText("Deleted")).toBeTruthy()
  })
})
