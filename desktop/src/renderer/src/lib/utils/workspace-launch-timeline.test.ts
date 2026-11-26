import { describe, expect, it } from "vitest"
import type { WorkspaceStatus } from "$lib/types/index.js"
import {
  createWorkspaceLaunchTimeline,
  reduceWorkspaceLaunchTimeline,
} from "./workspace-launch-timeline.js"

function event(overrides: Partial<WorkspaceStatus>): WorkspaceStatus {
  return {
    commandId: "cmd",
    workspaceId: "ws",
    phase: "building_image",
    state: "started",
    ...overrides,
  }
}

function apply(events: WorkspaceStatus[]) {
  return events.reduce(
    (timeline, item, index) =>
      reduceWorkspaceLaunchTimeline(timeline, item, index * 100),
    createWorkspaceLaunchTimeline(),
  )
}

describe("workspace launch timeline", () => {
  it("retains ordered lifecycle history and updates starts on completion", () => {
    const timeline = apply([
      event({ phase: "resolving_config", state: "started", operationId: "a" }),
      event({
        phase: "resolving_config",
        state: "succeeded",
        operationId: "a",
        durationMs: 20,
      }),
      event({
        phase: "preparing_devcontainer",
        state: "started",
        operationId: "b",
      }),
    ])
    expect(
      timeline.operations.map(({ phase, state }) => [phase, state]),
    ).toEqual([
      ["resolving_config", "succeeded"],
      ["preparing_devcontainer", "running"],
    ])
    expect(timeline.operations[0].firstSeenAtMs).toBe(0)
  })

  it("retains nested operation identity and parent links", () => {
    const timeline = apply([
      event({ phase: "preparing_devcontainer", operationId: "parent" }),
      event({
        phase: "building_image",
        operationId: "child",
        parentOperationId: "parent",
      }),
      event({
        phase: "building_image",
        state: "succeeded",
        operationId: "child",
        parentOperationId: "parent",
      }),
    ])
    expect(timeline.operations[1]).toMatchObject({
      key: "child",
      parentOperationId: "parent",
      state: "succeeded",
    })
    expect(timeline.operations[0].state).toBe("running")
  })

  it("keeps skipped and failed outcomes, including structured errors", () => {
    const timeline = apply([
      event({
        phase: "initialize_command",
        state: "skipped",
        step: "recovery mode",
      }),
      event({
        phase: "starting_container",
        state: "failed",
        operationId: "fail",
        error: { message: "Unavailable" },
      }),
    ])
    expect(timeline.operations.map((operation) => operation.state)).toEqual([
      "skipped",
      "failed",
    ])
    expect(timeline.operations[1].error?.message).toBe("Unavailable")
  })

  it("updates duplicate operation events and accepts completion without a start", () => {
    const timeline = apply([
      event({ operationId: "x", state: "succeeded", durationMs: 12 }),
      event({ operationId: "x", state: "succeeded", durationMs: 12 }),
      event({ phase: "launching_ide", state: "succeeded" }),
    ])
    expect(timeline.operations).toHaveLength(2)
    expect(timeline.operations[0].state).toBe("succeeded")
  })

  it("deduplicates terminal events without operation identifiers", () => {
    const timeline = apply([
      event({
        state: "skipped",
        phase: "initialize_command",
        step: "recovery mode",
      }),
      event({
        state: "skipped",
        phase: "initialize_command",
        step: "recovery mode",
      }),
      event({
        state: "failed",
        phase: "starting_container",
        step: "start",
        error: { message: "unavailable" },
      }),
      event({
        state: "failed",
        phase: "starting_container",
        step: "start",
        error: { message: "unavailable" },
      }),
    ])
    expect(timeline.operations).toHaveLength(2)
  })

  it("keeps identified operations distinct and ignores a late start after completion", () => {
    const timeline = apply([
      event({ operationId: "one", state: "started" }),
      event({ operationId: "one", state: "succeeded" }),
      event({ operationId: "one", state: "started" }),
      event({ operationId: "two", state: "started" }),
    ])
    expect(timeline.operations).toHaveLength(2)
    expect(timeline.operations.map((operation) => operation.state)).toEqual([
      "succeeded",
      "running",
    ])
  })

  it("matches missing identifiers and keeps orphan children visible", () => {
    const timeline = apply([
      event({ state: "started" }),
      event({ state: "succeeded", durationMs: 30 }),
      event({ phase: "building_image", parentOperationId: "missing" }),
    ])
    expect(timeline.operations).toHaveLength(2)
    expect(timeline.operations[0].state).toBe("succeeded")
    expect(timeline.operations[1].parentOperationId).toBe("missing")
  })
})
