// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest"
import { ProviderJobs } from "../provider-jobs.js"

describe("ProviderJobs", () => {
  let jobs: ProviderJobs

  beforeEach(() => {
    jobs = new ProviderJobs()
  })

  it("tracks activity and phase transitions", async () => {
    jobs.start("docker", "installing")
    expect(jobs.get("docker")).toEqual({ activity: "installing" })

    jobs.report("docker", "installing_provider")
    expect(jobs.get("docker")).toEqual({
      activity: "installing",
      phase: "installing_provider",
    })

    await jobs.finish("docker")
    expect(jobs.get("docker")).toBeUndefined()
  })

  it("ignores phase reports for a provider with no active job", () => {
    jobs.report("docker", "running_init")
    expect(jobs.get("docker")).toBeUndefined()
  })

  it("retains the failure so the UI can explain it", async () => {
    jobs.start("docker", "initializing")
    await jobs.finish("docker", "init: boom")

    expect(jobs.get("docker")).toEqual({
      activity: "initializing",
      phase: "failed",
      error: "init: boom",
    })
  })

  it("does not let a later release erase a recorded failure", async () => {
    jobs.start("docker", "initializing")
    await jobs.finish("docker", "init: boom")

    // The wizard closing after a failed init releases the job; that must not
    // turn the failure into a clean success.
    await jobs.finish("docker")

    expect(jobs.get("docker")?.error).toBe("init: boom")
  })

  it("refreshes provider state before clearing a finished job", async () => {
    // Clearing first would briefly expose initialized:false from the stale
    // list — the red badge this class exists to prevent.
    const order: string[] = []
    jobs.setRefresh(async () => {
      order.push(`refresh(job=${jobs.get("docker") ? "present" : "gone"})`)
    })

    jobs.start("docker", "installing")
    await jobs.finish("docker")

    expect(order).toEqual(["refresh(job=present)"])
    expect(jobs.get("docker")).toBeUndefined()
  })

  it("does not clear a newer job started while refresh was in flight", async () => {
    // refresh is a real CLI round-trip. If a second command starts during it,
    // the first finish() must not delete the entry the new one is using —
    // its report() calls would find no job and the card would look idle.
    let releaseRefresh: (() => void) | undefined
    jobs.setRefresh(
      () =>
        new Promise<void>((resolve) => {
          releaseRefresh = resolve
        }),
    )

    jobs.start("docker", "installing")
    const finishing = jobs.finish("docker")

    // A re-init lands before the first finish resolves.
    jobs.start("docker", "initializing")
    releaseRefresh?.()
    await finishing

    expect(jobs.get("docker")).toEqual({ activity: "initializing" })
  })

  it("still tracks phases for the superseding job", async () => {
    let releaseRefresh: (() => void) | undefined
    jobs.setRefresh(
      () =>
        new Promise<void>((resolve) => {
          releaseRefresh = resolve
        }),
    )

    jobs.start("docker", "installing")
    const finishing = jobs.finish("docker")
    jobs.start("docker", "initializing")
    releaseRefresh?.()
    await finishing

    jobs.report("docker", "running_init")

    expect(jobs.get("docker")?.phase).toBe("running_init")
  })

  it("notifies listeners on every mutation", () => {
    const listener = vi.fn()
    jobs.onChange(listener)

    jobs.start("docker", "installing")
    jobs.report("docker", "installing_provider")
    jobs.clear("docker")

    expect(listener).toHaveBeenCalledTimes(3)
  })

  it("does not notify when clearing an untracked provider", () => {
    const listener = vi.fn()
    jobs.onChange(listener)

    jobs.clear("nonexistent")

    expect(listener).not.toHaveBeenCalled()
  })

  it("clears a retained failure so a re-added provider starts clean", () => {
    jobs.start("docker", "installing")
    jobs.report("docker", "failed", "boom")
    jobs.clear("docker")

    expect(jobs.get("docker")).toBeUndefined()
  })
})
