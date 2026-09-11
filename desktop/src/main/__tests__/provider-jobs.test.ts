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

    jobs.reportStatus("docker", {
      phase: "installing_provider",
      state: "started",
    })
    expect(jobs.get("docker")).toEqual({
      activity: "installing",
      phase: "installing_provider",
      state: "started",
    })

    await jobs.finish("docker")
    expect(jobs.get("docker")).toBeUndefined()
  })

  it("ignores phase reports for a provider with no active job", () => {
    jobs.reportStatus("docker", {
      phase: "running_init",
      state: "started",
    })
    expect(jobs.get("docker")).toBeUndefined()
  })

  it("retains the complete structured status event", () => {
    jobs.start("docker", "initializing")
    jobs.reportStatus("docker", {
      phase: "running_init",
      state: "failed",
      operationId: "op-17",
      parentOperationId: "op-1",
      durationMs: 312,
      error: {
        code: "docker_daemon_unreachable",
        message: "Docker daemon is unavailable.",
        hint: "Start Docker and retry.",
        context: { context: "desktop-linux" },
      },
    })

    expect(jobs.get("docker")).toMatchObject({
      phase: "running_init",
      state: "failed",
      operationId: "op-17",
      parentOperationId: "op-1",
      durationMs: 312,
      error: "Docker daemon is unavailable.",
      errorCode: "docker_daemon_unreachable",
      errorHint: "Start Docker and retry.",
      errorContext: { context: "desktop-linux" },
    })
  })

  it("retains the failure so the UI can explain it", async () => {
    jobs.start("docker", "initializing")
    await jobs.finish("docker", "init: boom")

    expect(jobs.get("docker")).toEqual({
      activity: "initializing",
      phase: "failed",
      state: "failed",
      error: "init: boom",
      errorCode: undefined,
      errorHint: undefined,
      errorContext: undefined,
    })
  })

  it("retains structured error metadata when finishing", async () => {
    jobs.start("docker", "initializing")
    await jobs.finish("docker", {
      code: "docker_daemon_unreachable",
      message: "Docker is unavailable.",
      hint: "Start Docker and retry.",
      context: { socket: "desktop-linux" },
    })

    expect(jobs.get("docker")).toMatchObject({
      state: "failed",
      error: "Docker is unavailable.",
      errorCode: "docker_daemon_unreachable",
      errorHint: "Start Docker and retry.",
      errorContext: { socket: "desktop-linux" },
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

    jobs.reportStatus("docker", { phase: "running_init", state: "started" })

    expect(jobs.get("docker")?.phase).toBe("running_init")
  })

  it("notifies listeners on every mutation", () => {
    const listener = vi.fn()
    jobs.onChange(listener)

    jobs.start("docker", "installing")
    jobs.reportStatus("docker", {
      phase: "installing_provider",
      state: "started",
    })
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
    jobs.reportStatus("docker", {
      phase: "failed",
      state: "failed",
      error: { message: "boom" },
    })
    jobs.clear("docker")

    expect(jobs.get("docker")).toBeUndefined()
  })
})
