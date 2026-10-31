import { cleanup, fireEvent, render, waitFor } from "@testing-library/svelte"
import type { ComponentProps } from "svelte"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { mockInvoke, resetTauriMocks } from "$lib/__mocks__/tauri.js"
import {
  MOCK_INV_POST_CREATE_FAILED,
  MOCK_INV_POST_CREATE_OK,
  MOCK_INV_SILENT_OK,
  MOCK_INV_UNCAPTURED,
  MOCK_RUN_FAILED,
  MOCK_RUN_OK,
  MOCK_RUN_PARTIAL,
  MOCK_RUN_REMOVED,
  mockLogRunRead,
  mockLogRuns,
} from "$lib/ipc/mock.js"
import { toasts } from "$lib/stores/toasts.js"
import WorkspaceRunLogs from "./WorkspaceRunLogs.svelte"

vi.mock("$lib/stores/toasts.js", () => ({
  toasts: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}))

afterEach(cleanup)
beforeEach(() => {
  resetTauriMocks()
  vi.clearAllMocks()
  mockInvoke.mockImplementation(
    async (cmd: string, args?: Record<string, unknown>) => {
      if (cmd === "workspace_log_runs") return mockLogRuns(args ?? {})
      if (cmd === "workspace_log_run_read") return mockLogRunRead(args ?? {})
      if (cmd === "workspace_log_run_cancel") return undefined
      if (cmd === "workspace_log_run_export") return { status: "canceled" }
      throw new Error(`unexpected ${cmd}`)
    },
  )
})

const base = {
  context: "default",
  workspaceId: "my-project",
  workspaceUid: "ws-001",
}

// "context" is also a Svelte mount option, so props must be nested.
const mount = (props: ComponentProps<typeof WorkspaceRunLogs>) =>
  render(WorkspaceRunLogs, { props })

function reads() {
  return mockInvoke.mock.calls.filter((c) => c[0] === "workspace_log_run_read")
}

describe("WorkspaceRunLogs", () => {
  it("defaults to the latest run and lists its steps with outcomes", async () => {
    const ui = mount(base)
    await waitFor(() => expect(ui.getByText("Failed")).toBeTruthy())
    expect(reads()[0][1]).toMatchObject({
      runId: MOCK_RUN_FAILED,
      context: "default",
    })
    expect(ui.getByText("Skipped")).toBeTruthy()
    expect(ui.getByText("Already attempted, not run again")).toBeTruthy()
    expect(ui.getByText(/exit code 1/)).toBeTruthy()
  })

  it("shows a skipped step reason even with zero output", async () => {
    const ui = mount({
      ...base,
      pinnedRunId: MOCK_RUN_FAILED,
      pinnedInvocationId: "a1111111-0000-4000-8000-000000000002",
    })
    await waitFor(() =>
      expect(
        ui.getAllByText("Already attempted, not run again").length,
      ).toBeGreaterThan(1),
    )
  })

  it("distinguishes silent success from output that was never captured", async () => {
    const ok = mount({
      ...base,
      pinnedRunId: MOCK_RUN_OK,
      pinnedInvocationId: MOCK_INV_SILENT_OK,
    })
    await waitFor(() =>
      expect(
        ok.getByText("Completed with no output (captured, 0 bytes)."),
      ).toBeTruthy(),
    )
    cleanup()
    const partial = mount({
      ...base,
      pinnedRunId: MOCK_RUN_PARTIAL,
      pinnedInvocationId: MOCK_INV_UNCAPTURED,
    })
    await waitFor(() =>
      expect(
        partial.getByText("Output was not captured for this step."),
      ).toBeTruthy(),
    )
  })

  it("renders deferred hooks and successful output as literal text", async () => {
    const ui = mount({
      ...base,
      pinnedRunId: MOCK_RUN_OK,
      pinnedInvocationId: MOCK_INV_POST_CREATE_OK,
    })
    await waitFor(() =>
      expect(ui.getByText("installed 12 packages")).toBeTruthy(),
    )
    expect(ui.getByText("no trailing newline")).toBeTruthy()
    expect(ui.getByText("Deferred")).toBeTruthy()
  })

  it("opens a failing invocation directly and keeps JSON and HTML literal", async () => {
    const ui = mount({
      ...base,
      pinnedRunId: MOCK_RUN_FAILED,
      pinnedInvocationId: MOCK_INV_POST_CREATE_FAILED,
    })
    await waitFor(() =>
      expect(
        ui.getByText('{"level":"error","msg":"not a log line"}'),
      ).toBeTruthy(),
    )
    expect(ui.getByText("<b>literal</b>")).toBeTruthy()
    expect(ui.container.querySelector("b")).toBeNull()
  })

  it("falls back to the run's failed steps when the invocation is unknown", async () => {
    const ui = mount({
      ...base,
      pinnedRunId: MOCK_RUN_FAILED,
      pinnedInvocationId: "a9999999-0000-4000-8000-000000000009",
    })
    await waitFor(() =>
      expect(ui.getByText(/The linked step is not in this run/)).toBeTruthy(),
    )
    await waitFor(() => expect(ui.getByText("npm install")).toBeTruthy())
  })

  it("keeps an explicit older run when a new operation run arrives", async () => {
    const ui = mount(base)
    await waitFor(() => expect(ui.getByText("Failed")).toBeTruthy())
    const select = ui.getByLabelText("Run") as HTMLSelectElement
    await fireEvent.change(select, { target: { value: MOCK_RUN_OK } })
    await waitFor(() => expect(ui.getByText("Deferred")).toBeTruthy())
    await ui.rerender({
      operationRunId: "55555555-5555-4555-8555-555555555555",
    })
    await waitFor(() => expect(select.value).toBe(MOCK_RUN_OK))
  })

  it("renders a read-only branch for a removed workspace with stored identity", async () => {
    const ui = mount({
      context: "default",
      readOnly: true,
      pinnedRunId: MOCK_RUN_REMOVED,
    })
    await waitFor(() => expect(ui.getByText(/Read-only history/)).toBeTruthy())
    expect(ui.getByText(/deleted-project/)).toBeTruthy()
    const [, args] = reads()[0]
    expect(args).toMatchObject({ runId: MOCK_RUN_REMOVED })
    expect(args.workspaceId).toBeUndefined()
    expect(args.workspaceUid).toBeUndefined()
    const listCall = mockInvoke.mock.calls.find(
      (c) => c[0] === "workspace_log_runs",
    )
    expect(listCall?.[1].workspaceId).toBeUndefined()
  })

  it("asks only for readers to cancel when unmounted", async () => {
    const ui = mount(base)
    await waitFor(() => expect(ui.getByText("Failed")).toBeTruthy())
    ui.unmount()
    const channels = mockInvoke.mock.calls.map((c) => c[0])
    expect(channels).toContain("workspace_log_run_cancel")
    expect(channels).not.toContain("workspace_cancel")
  })

  it("exports through the main process and treats cancel as normal", async () => {
    const ui = mount(base)
    await waitFor(() => expect(ui.getByText("Failed")).toBeTruthy())
    await fireEvent.click(ui.getByRole("button", { name: "Export" }))
    await waitFor(() =>
      expect(mockInvoke).toHaveBeenCalledWith("workspace_log_run_export", {
        context: "default",
        runId: MOCK_RUN_FAILED,
        workspaceId: "my-project",
        workspaceUid: "ws-001",
        invocationId: undefined,
      }),
    )
    expect(toasts.error).not.toHaveBeenCalled()
  })

  it("explains an empty history", async () => {
    mockInvoke.mockImplementation(async (cmd: string) =>
      cmd === "workspace_log_runs"
        ? { schemaVersion: 1, runs: [], hasMore: false }
        : undefined,
    )
    const ui = mount(base)
    await waitFor(() =>
      expect(ui.getByText(/No retained setup history/)).toBeTruthy(),
    )
  })
})
