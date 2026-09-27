import { createRequire } from "node:module"
import { describe, expect, it } from "vitest"

const require = createRequire(import.meta.url)
const { mergeTasks } = require("../../../e2e/fixtures/mock-task-state.cjs") as {
  mergeTasks: (
    current: Record<string, { status: string }>,
    update: Record<string, { status: string }>,
    deletedIds?: string[],
  ) => Record<string, { status: string }>
}

describe("mock detached task persistence", () => {
  it("preserves tasks created after a process loaded its state snapshot", () => {
    const current = { "task-new": { status: "pending" } }
    const staleUpdate = { "task-old": { status: "succeeded" } }

    expect(mergeTasks(current, staleUpdate)).toEqual({
      "task-new": { status: "pending" },
      "task-old": { status: "succeeded" },
    })
  })

  it("does not let a stale pending snapshot undo task cancellation", () => {
    expect(
      mergeTasks(
        { "task-1": { status: "failed" } },
        { "task-1": { status: "pending" } },
      ),
    ).toEqual({ "task-1": { status: "failed" } })
  })

  it("does not let stale completion overwrite task cancellation", () => {
    expect(
      mergeTasks(
        { "task-1": { status: "failed" } },
        { "task-1": { status: "succeeded" } },
      ),
    ).toEqual({ "task-1": { status: "failed" } })
  })

  it("applies explicit task removal", () => {
    expect(
      mergeTasks(
        { "task-1": { status: "succeeded" } },
        {},
        ["task-1"],
      ),
    ).toEqual({})
  })
})
