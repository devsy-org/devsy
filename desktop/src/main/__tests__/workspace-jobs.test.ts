// @vitest-environment node
import { describe, expect, it, vi } from "vitest"
import { WorkspaceJobs } from "../workspace-jobs.js"

describe("WorkspaceJobs", () => {
  it("publishes action state independently of runtime status", async () => {
    const jobs = new WorkspaceJobs()
    const changed = vi.fn()
    jobs.onChange(changed)
    const generation = jobs.start("ws", "stopping", "command")
    expect(jobs.get("ws")).toMatchObject({
      commandId: "command",
      activity: "stopping",
      state: "running",
    })
    jobs.progress("ws", "command", {
      phase: "stopping_workspace",
      step: "Waiting for lock",
      state: "started",
    })
    jobs.progress("ws", "command", {
      phase: "stopping_workspace",
      state: "succeeded",
    })
    expect(jobs.get("ws")).toMatchObject({
      state: "running",
      phase: "Waiting for lock",
    })
    await jobs.finish("ws", generation)
    expect(jobs.get("ws")?.state).toBe("succeeded")
    expect(changed).toHaveBeenCalled()
  })

  it("rejects duplicates and allows delete to supersede a start", async () => {
    const jobs = new WorkspaceJobs()
    const first = jobs.start("ws", "starting", "first")
    expect(() => jobs.start("ws", "rebuilding", "conflict")).toThrow(
      "already in progress",
    )
    jobs.start("ws", "deleting", "delete")
    jobs.progress("ws", "first", { phase: "ready", state: "succeeded" })
    await jobs.finish("ws", first, "old failure")
    expect(jobs.get("ws")).toMatchObject({
      commandId: "delete",
      state: "running",
    })
  })

  it("retains a completed delete on refresh failure and retries refresh only", async () => {
    const jobs = new WorkspaceJobs()
    const refresh = vi
      .fn()
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValue(undefined)
    jobs.setRefresh(refresh)
    const generation = jobs.start("ws", "deleting", "delete")
    await jobs.finish("ws", generation)
    expect(jobs.get("ws")).toMatchObject({
      state: "reconciling",
      refreshError: "offline",
    })
    expect(() => jobs.start("ws")).toThrow()
    await jobs.retryRefresh("ws")
    expect(jobs.get("ws")).toMatchObject({
      state: "succeeded",
      refreshError: undefined,
    })
    expect(refresh).toHaveBeenCalledTimes(2)
  })

  it("keeps the action visible until reconciliation finishes and ignores duplicate completion", async () => {
    const jobs = new WorkspaceJobs()
    let release!: () => void
    jobs.setRefresh(
      () =>
        new Promise<void>((resolve) => {
          release = resolve
        }),
    )
    const generation = jobs.start("ws")
    const finishing = jobs.finish("ws", generation, "denied")
    expect(jobs.get("ws")).toMatchObject({
      state: "reconciling",
      error: "denied",
    })
    await jobs.finish("ws", generation)
    release()
    await finishing
    expect(jobs.get("ws")).toMatchObject({ state: "failed", error: "denied" })
    expect(() => jobs.start("ws")).not.toThrow()
  })

  it("records a failed command even when refresh also fails", async () => {
    const jobs = new WorkspaceJobs()
    const refresh = vi
      .fn()
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce(undefined)
    jobs.setRefresh(refresh)
    const generation = jobs.start("ws", "stopping", "stop")
    await jobs.finish("ws", generation, "command denied")
    expect(jobs.get("ws")).toMatchObject({
      state: "failed",
      error: "command denied",
      refreshError: "offline",
    })
    await jobs.retryRefresh("ws")
    expect(jobs.get("ws")).toMatchObject({ state: "failed", error: "command denied" })
  })

  it("invalidates polls on acceptance and completion", async () => {
    const jobs = new WorkspaceJobs()
    const before = jobs.generation("ws")
    const generation = jobs.start("ws")
    expect(generation).not.toBe(before)
    await jobs.finish("ws", generation)
    expect(jobs.generation("ws")).not.toBe(generation)
  })
})
