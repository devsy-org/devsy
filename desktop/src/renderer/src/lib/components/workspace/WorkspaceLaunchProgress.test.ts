import { cleanup, fireEvent, render } from "@testing-library/svelte"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { WorkspaceLaunchTimeline } from "$lib/utils/workspace-launch-timeline.js"
import WorkspaceLaunchProgress from "./WorkspaceLaunchProgress.svelte"

afterEach(cleanup)

function operation(
  overrides: Partial<WorkspaceLaunchTimeline["operations"][number]>,
) {
  return {
    key: "parent",
    operationId: "parent",
    phase: "preparing_devcontainer",
    state: "running" as const,
    firstSeenAtMs: 0,
    ...overrides,
  }
}

describe("WorkspaceLaunchProgress", () => {
  it("shows running state, elapsed time, and active child detail", () => {
    const { container, getByText } = render(WorkspaceLaunchProgress, {
      props: {
        workspaceId: "demo",
        provider: "docker",
        ideLabel: "VS Code",
        timeline: {
          operations: [
            operation({}),
            operation({
              key: "child",
              operationId: "child",
              parentOperationId: "parent",
              phase: "building_image",
            }),
            operation({
              key: "nested",
              operationId: "nested",
              parentOperationId: "child",
              phase: "starting_container",
            }),
          ],
        },
        running: true,
        confirmationState: "running",
        elapsedMs: 38000,
        logsAvailable: false,
        logsOpen: false,
        onToggleLogs: vi.fn(),
      },
    })
    expect(getByText("Launching workspace")).toBeTruthy()
    expect(getByText("0:38")).toBeTruthy()
    expect(getByText("Starting container…")).toBeTruthy()
    expect(container.querySelector("[aria-busy='true']")).toBeTruthy()
  })

  it("announces one failure and exposes the logs disclosure", async () => {
    const onToggleLogs = vi.fn()
    const { getByText, container } = render(WorkspaceLaunchProgress, {
      props: {
        workspaceId: "demo",
        timeline: {
          operations: [
            operation({
              state: "failed",
              error: { message: "daemon unavailable" },
            }),
            operation({
              key: "child",
              operationId: "child",
              parentOperationId: "parent",
              phase: "building_image",
              state: "failed",
              error: { message: "daemon unavailable" },
            }),
          ],
        },
        running: false,
        confirmationState: "failed",
        error: "daemon unavailable",
        elapsedMs: 1000,
        logsAvailable: true,
        logsOpen: false,
        onToggleLogs,
      },
    })
    expect(container.textContent?.match(/daemon unavailable/g)).toHaveLength(1)
    expect(getByText("daemon unavailable").getAttribute("aria-live")).toBe(
      "assertive",
    )
    await fireEvent.click(getByText("View logs"))
    expect(onToggleLogs).toHaveBeenCalledOnce()
  })

  it("labels skipped and successful operations and humanizes unknown phases", () => {
    const { getByText } = render(WorkspaceLaunchProgress, {
      props: {
        workspaceId: "demo",
        timeline: {
          operations: [
            operation({ state: "succeeded", durationMs: 2500 }),
            operation({
              key: "skip",
              operationId: "skip",
              phase: "unknown_future_phase",
              state: "skipped",
              step: "not required",
            }),
          ],
        },
        running: false,
        confirmationState: "confirmed",
        elapsedMs: 0,
        logsAvailable: false,
        logsOpen: false,
        onToggleLogs: vi.fn(),
      },
    })
    expect(getByText("Workspace ready")).toBeTruthy()
    expect(getByText("2.5s")).toBeTruthy()
    expect(getByText(/Skipped: Unknown future phase/)).toBeTruthy()
    expect(getByText("not required")).toBeTruthy()
  })

  it("keeps top-level operations visible when some events lack operation IDs", () => {
    const { getByText } = render(WorkspaceLaunchProgress, {
      props: {
        workspaceId: "demo",
        timeline: {
          operations: [
            operation({ key: "prepare", operationId: "prepare" }),
            operation({
              key: "legacy",
              operationId: undefined,
              phase: "starting_container",
              state: "succeeded",
            }),
          ],
        },
        running: false,
        confirmationState: "running",
        elapsedMs: 0,
        logsAvailable: false,
        logsOpen: false,
        onToggleLogs: vi.fn(),
      },
    })
    expect(getByText("Preparing dev container")).toBeTruthy()
    expect(getByText("Starting container")).toBeTruthy()
  })

  it("shows the error for a failed operation without an operation ID", () => {
    const { getByText } = render(WorkspaceLaunchProgress, {
      props: {
        workspaceId: "demo",
        timeline: {
          operations: [
            operation({
              key: "legacy-failure",
              operationId: undefined,
              state: "failed",
              error: { message: "legacy failure" },
            }),
          ],
        },
        running: false,
        confirmationState: "failed",
        elapsedMs: 0,
        logsAvailable: false,
        logsOpen: false,
        onToggleLogs: vi.fn(),
      },
    })
    expect(getByText("legacy failure")).toBeTruthy()
  })

  it("shows the deepest failed descendant and its error once", () => {
    const { getByText, getAllByText, queryByText } = render(
      WorkspaceLaunchProgress,
      {
        props: {
          workspaceId: "demo",
          timeline: {
            operations: [
              operation({
                state: "failed",
                error: { message: "parent failure" },
              }),
              operation({
                key: "start",
                operationId: "start",
                parentOperationId: "parent",
                phase: "starting_container",
                state: "failed",
              }),
              operation({
                key: "build",
                operationId: "build",
                parentOperationId: "start",
                phase: "building_image",
                state: "failed",
                error: { message: "image build failed" },
              }),
            ],
          },
          running: false,
          confirmationState: "failed",
          error: "overall failure",
          elapsedMs: 0,
          logsAvailable: false,
          logsOpen: false,
          onToggleLogs: vi.fn(),
        },
      },
    )

    expect(getByText(/Failed: Building image/)).toBeTruthy()
    expect(getAllByText("image build failed")).toHaveLength(1)
    expect(queryByText("parent failure")).toBeNull()
    expect(queryByText("overall failure")).toBeNull()
  })

  it("shows nested operation details while they run", () => {
    const { getByText } = render(WorkspaceLaunchProgress, {
      props: {
        workspaceId: "demo",
        timeline: {
          operations: [
            operation({}),
            operation({
              key: "start",
              operationId: "start",
              parentOperationId: "parent",
              phase: "starting_container",
              state: "running",
            }),
            operation({
              key: "build",
              operationId: "build",
              parentOperationId: "start",
              phase: "building_image",
              state: "running",
            }),
          ],
        },
        running: true,
        confirmationState: "running",
        elapsedMs: 0,
        logsAvailable: false,
        logsOpen: false,
        onToggleLogs: vi.fn(),
      },
    })
    expect(getByText("Starting container…")).toBeTruthy()
    expect(getByText("Building image…")).toBeTruthy()
  })

  it("shows completed nested operation phases", () => {
    const { getByText } = render(WorkspaceLaunchProgress, {
      props: {
        workspaceId: "demo",
        timeline: {
          operations: [
            operation({ state: "succeeded" }),
            operation({
              key: "start",
              operationId: "start",
              parentOperationId: "parent",
              phase: "starting_container",
              state: "succeeded",
            }),
            operation({
              key: "build",
              operationId: "build",
              parentOperationId: "start",
              phase: "building_image",
              state: "succeeded",
            }),
          ],
        },
        running: false,
        confirmationState: "confirmed",
        elapsedMs: 0,
        logsAvailable: false,
        logsOpen: false,
        onToggleLogs: vi.fn(),
      },
    })
    expect(getByText("Preparing dev container")).toBeTruthy()
    expect(getByText("Completed: Starting container")).toBeTruthy()
    expect(getByText("Completed: Building image")).toBeTruthy()
  })
})
