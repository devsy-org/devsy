// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest"
import { WorkspaceJobs } from "../workspace-jobs.js"

describe("WorkspaceJobs", () => {
  let jobs: WorkspaceJobs

  beforeEach(() => {
    jobs = new WorkspaceJobs()
  })

  it("tracks a delete until it finishes", async () => {
    const generation = jobs.start("ws1")
    expect(jobs.get("ws1")).toEqual({ activity: "deleting" })

    await jobs.finish("ws1", generation)
    expect(jobs.get("ws1")).toBeUndefined()
  })

  it("does not let a stale failure land on a newer job", async () => {
    const first = jobs.start("ws1")
    const second = jobs.start("ws1")

    // A slow/duplicate exit callback for the first delete arrives after a
    // retry has already started a second one for the same workspace.
    await jobs.finish("ws1", first, "boom")

    expect(jobs.get("ws1")).toEqual({ activity: "deleting" })
    expect(second).not.toBe(first)
  })

  it("retains the failure so the UI can explain it", async () => {
    const generation = jobs.start("ws1")
    await jobs.finish("ws1", generation, "delete exited with code 1")

    expect(jobs.get("ws1")).toEqual({
      activity: "deleting",
      error: "delete exited with code 1",
    })
  })

  it("does not let a later release erase a recorded failure", async () => {
    const generation = jobs.start("ws1")
    await jobs.finish("ws1", generation, "boom")

    await jobs.finish("ws1", generation)

    expect(jobs.get("ws1")?.error).toBe("boom")
  })

  it("ignores a failure for a workspace with no active job", async () => {
    // e.g. a stale exit callback firing after clear() already ran.
    await jobs.finish("ws1", 1, "boom")
    expect(jobs.get("ws1")).toBeUndefined()
  })

  it("refreshes the workspace list before clearing a finished job", async () => {
    // Clearing first would briefly show the deleted workspace as still
    // present from the stale list.
    const order: string[] = []
    jobs.setRefresh(async () => {
      order.push(`refresh(job=${jobs.get("ws1") ? "present" : "gone"})`)
    })

    const generation = jobs.start("ws1")
    await jobs.finish("ws1", generation)

    expect(order).toEqual(["refresh(job=present)"])
    expect(jobs.get("ws1")).toBeUndefined()
  })

  it("does not clear a newer job started while refresh was in flight", async () => {
    let releaseRefresh: (() => void) | undefined
    jobs.setRefresh(
      () =>
        new Promise<void>((resolve) => {
          releaseRefresh = resolve
        }),
    )

    const generation = jobs.start("ws1")
    const finishing = jobs.finish("ws1", generation)

    // A new delete for the same id is submitted before the first finish
    // resolves (e.g. a quick retry after the first attempt looked stuck).
    jobs.start("ws1")
    releaseRefresh?.()
    await finishing

    expect(jobs.get("ws1")).toEqual({ activity: "deleting" })
  })

  it("notifies listeners on every mutation", () => {
    const listener = vi.fn()
    jobs.onChange(listener)

    jobs.start("ws1")
    jobs.clear("ws1")

    expect(listener).toHaveBeenCalledTimes(2)
  })

  it("does not notify when clearing an untracked workspace", () => {
    const listener = vi.fn()
    jobs.onChange(listener)

    jobs.clear("nonexistent")

    expect(listener).not.toHaveBeenCalled()
  })
})
