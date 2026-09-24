import { cleanup, fireEvent, render, waitFor } from "@testing-library/svelte"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { mockInvoke, resetTauriMocks } from "$lib/__mocks__/tauri.js"
import { toasts } from "$lib/stores/toasts.js"
import { workspaceJobs } from "$lib/stores/workspaces.js"
import WorkspaceOperation from "./WorkspaceOperation.svelte"

vi.mock("$lib/stores/toasts.js", () => ({
  toasts: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}))
vi.mock("$lib/router.js", () => ({
  goto: vi.fn(),
  push: vi.fn(),
  replace: vi.fn(),
  router: {},
  location: { subscribe: () => () => {} },
  querystring: { subscribe: () => () => {} },
}))
afterEach(cleanup)
beforeEach(() => {
  resetTauriMocks()
  workspaceJobs.set({})
  vi.clearAllMocks()
})
describe("WorkspaceOperation", () => {
  it("renders compact lifecycle status as a single-line badge", () => {
    const ui = render(WorkspaceOperation, {
      id: "ws",
      status: "Running",
      density: "compact",
    })
    expect(ui.getByText("Running")).toBeTruthy()
    const region = ui.getByRole("status")
    expect(region.getAttribute("aria-busy")).toBe("false")
    expect(region.querySelector('[aria-hidden="true"]')).toBeNull()
    expect(
      region.querySelector('[data-slot="workspace-operation-detail"]'),
    ).toBeNull()
    expect(region.classList.contains("min-h-10")).toBe(false)
  })
  it("keeps the active command ahead of a stale runtime observation", () => {
    workspaceJobs.set({
      ws: {
        commandId: "delete",
        activity: "deleting",
        state: "running",
        phase: "closing_connections",
      },
    })
    const ui = render(WorkspaceOperation, {
      id: "ws",
      status: "Running",
      density: "compact",
    })
    expect(ui.getByText("Deleting")).toBeTruthy()
    expect(ui.queryByText("Closing connections")).toBeNull()
    expect(ui.queryByText("Running")).toBeNull()
  })
  it("shows the delete headline without confirmation details in compact density", () => {
    workspaceJobs.set({
      ws: {
        commandId: "delete",
        activity: "deleting",
        state: "reconciling",
        phase: "Refreshing list",
      },
    })
    const ui = render(WorkspaceOperation, {
      id: "ws",
      status: "Running",
      density: "compact",
    })
    expect(ui.getByText("Deleting")).toBeTruthy()
    expect(ui.queryByText("Confirming removal")).toBeNull()
    expect(ui.queryByText("Deleted")).toBeNull()
    expect(ui.getByRole("status").getAttribute("aria-busy")).toBe("true")
  })
  it("retains the operation phase in expanded density", () => {
    workspaceJobs.set({
      ws: {
        commandId: "delete",
        activity: "deleting",
        state: "reconciling",
        phase: "Refreshing list",
      },
    })
    const ui = render(WorkspaceOperation, {
      id: "ws",
      status: "Running",
      density: "expanded",
    })
    expect(ui.getByText("Deleting")).toBeTruthy()
    expect(ui.getByText("Confirming removal")).toBeTruthy()
    expect(
      ui.getByRole("status").querySelector('[data-slot="workspace-operation-detail"]'),
    ).toBeTruthy()
  })
  it("shows recovery wording with an expanded Retry that only re-refreshes", async () => {
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
    const ui = render(WorkspaceOperation, {
      id: "ws",
      status: "Running",
      density: "expanded",
    })
    expect(ui.getByText(/List may be out of date/)).toBeTruthy()
    expect(ui.queryByText("Deleted")).toBeNull()
    expect(ui.getByRole("status").getAttribute("aria-busy")).toBe("false")
    await fireEvent.click(
      ui.getByRole("button", { name: "Retry status for ws" }),
    )
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
  })
  it("renders an operation failure with the error and View logs when expanded", async () => {
    workspaceJobs.set({
      ws: {
        commandId: "stop",
        activity: "stopping",
        state: "failed",
        phase: "stopping_workspace",
        error: "provider unavailable",
      },
    })
    const onViewLogs = vi.fn()
    const ui = render(WorkspaceOperation, {
      id: "ws",
      status: "Running",
      density: "expanded",
      onViewLogs,
    })
    expect(ui.getByText("Stop failed")).toBeTruthy()
    expect(ui.getByText(/provider unavailable/)).toBeTruthy()
    expect(ui.getByRole("status").getAttribute("aria-live")).toBe("assertive")
    await fireEvent.click(ui.getByRole("button", { name: "View logs for ws" }))
    expect(onViewLogs).toHaveBeenCalledOnce()
  })
  it("does not nest a View logs button in compact density", () => {
    workspaceJobs.set({
      ws: {
        commandId: "stop",
        activity: "stopping",
        state: "failed",
        phase: "stopping_workspace",
        error: "provider unavailable",
      },
    })
    const ui = render(WorkspaceOperation, { id: "ws", status: "Running" })
    expect(ui.getByText("Stop failed")).toBeTruthy()
    expect(ui.queryByText(/provider unavailable/)).toBeNull()
    expect(ui.queryByRole("button", { name: "View logs for ws" })).toBeNull()
  })

  it("does not nest a Retry button in compact density", () => {
    workspaceJobs.set({
      ws: {
        commandId: "delete",
        activity: "deleting",
        state: "reconciling",
        phase: "Refreshing list",
        refreshError: "offline",
      },
    })
    const ui = render(WorkspaceOperation, { id: "ws", status: "Running" })

    expect(ui.getByText("Deleting")).toBeTruthy()
    expect(ui.queryByText(/List may be out of date/)).toBeNull()
    expect(ui.queryByRole("button", { name: "Retry status for ws" })).toBeNull()
  })

  it("announces compact operation errors assertively", () => {
    workspaceJobs.set({
      ws: {
        commandId: "stop",
        activity: "stopping",
        state: "failed",
        phase: "stopping_workspace",
        error: "provider unavailable",
      },
    })
    const ui = render(WorkspaceOperation, { id: "ws", status: "Running" })

    expect(ui.getByRole("status").getAttribute("aria-live")).toBe("assertive")
  })
})
