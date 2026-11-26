import { describe, expect, it } from "vitest"
import {
  presentLaunchOperation,
  topLevelLaunchOperations,
} from "./workspace-launch-presentation.js"
import type { LaunchOperation } from "./workspace-launch-timeline.js"

function operation(overrides: Partial<LaunchOperation> = {}): LaunchOperation {
  return {
    key: "prepare",
    operationId: "prepare",
    phase: "preparing_devcontainer",
    state: "running",
    firstSeenAtMs: 0,
    ...overrides,
  }
}

describe("workspace launch presentation", () => {
  it("selects the deepest failed descendant before a running descendant", () => {
    const parent = operation({ state: "failed" })
    const timeline = {
      operations: [
        parent,
        operation({
          key: "start",
          operationId: "start",
          parentOperationId: "prepare",
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
        operation({
          key: "inject",
          operationId: "inject",
          parentOperationId: "prepare",
          phase: "injecting_agent",
          state: "running",
        }),
      ],
    }

    expect(presentLaunchOperation(parent, timeline).detail?.phase).toBe(
      "building_image",
    )
    expect(presentLaunchOperation(parent, timeline).errorMessage).toBe(
      "image build failed",
    )
  })

  it("prefers depth and then the most recently observed descendant", () => {
    const parent = operation()
    const timeline = {
      operations: [
        parent,
        operation({
          key: "first",
          operationId: "first",
          parentOperationId: "prepare",
          phase: "starting_container",
        }),
        operation({
          key: "nested",
          operationId: "nested",
          parentOperationId: "first",
          phase: "building_image",
        }),
        operation({
          key: "nested-latest",
          operationId: "nested-latest",
          parentOperationId: "first",
          phase: "running_command",
        }),
        operation({
          key: "last",
          operationId: "last",
          parentOperationId: "prepare",
          phase: "injecting_agent",
        }),
      ],
    }

    expect(presentLaunchOperation(parent, timeline).detail?.phase).toBe(
      "running_command",
    )
  })

  it("retains completed nested operations for presentation", () => {
    const parent = operation({ state: "succeeded" })
    const child = operation({
      key: "start",
      operationId: "start",
      parentOperationId: "prepare",
      phase: "starting_container",
      state: "succeeded",
    })
    const nested = operation({
      key: "build",
      operationId: "build",
      parentOperationId: "start",
      phase: "building_image",
      state: "failed",
    })
    const timeline = { operations: [parent, child, nested] }

    expect(
      presentLaunchOperation(parent, timeline).descendants.map(
        ({ operation: item }) => item.phase,
      ),
    ).toEqual(["starting_container", "building_image"])
  })

  it("keeps orphan operations visible and terminates on malformed cycles", () => {
    const first = operation({ parentOperationId: "second" })
    const second = operation({
      key: "second",
      operationId: "second",
      parentOperationId: "prepare",
      phase: "starting_container",
    })
    const orphan = operation({
      key: "orphan",
      operationId: "orphan",
      parentOperationId: "missing",
      phase: "building_image",
    })
    const timeline = { operations: [first, second, orphan] }

    expect(topLevelLaunchOperations(timeline)).toContain(orphan)
    expect(presentLaunchOperation(first, timeline).detail).toBe(second)
  })
})
