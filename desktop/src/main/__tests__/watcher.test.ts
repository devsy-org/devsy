import { describe, expect, it, vi } from "vitest"
import { Watcher } from "../watcher.js"

function makeWatcher(
  runProviderList: () => Promise<Record<string, unknown>>,
  workspaces: Array<{ id: string; status?: string }> = [],
  runRaw: (args: string[]) => Promise<unknown> = async () => ({}),
) {
  const state = {
    updateProviders: vi.fn().mockReturnValue(false),
    providerList: vi.fn().mockReturnValue([]),
    workspaceList: vi.fn().mockReturnValue(workspaces),
    updateWorkspaceStatus: vi.fn((id: string, status: string) => {
      const workspace = workspaces.find((item) => item.id === id)
      if (!workspace || workspace.status === status) return false
      workspace.status = status
      return true
    }),
  }
  const cli = {
    run: vi.fn((args: string[]) => {
      if (args[0] === "provider" && args[1] === "list") {
        return runProviderList()
      }
      return Promise.resolve([])
    }),
    runRaw: vi.fn(runRaw),
  }
  const providerJobs = { snapshot: vi.fn().mockReturnValue({}) }
  const workspaceJobs = { snapshot: vi.fn().mockReturnValue({}) }
  const watcher = new Watcher({
    cli: cli as never,
    state: state as never,
    getMainWindow: () => null,
    providerJobs: providerJobs as never,
    workspaceJobs: workspaceJobs as never,
  })
  return { watcher, cli, state }
}

describe("Watcher.refreshProviders", () => {
  it("does not run concurrently with another in-flight provider query", async () => {
    let inFlight = 0
    let concurrentCalls = 0
    const { watcher } = makeWatcher(async () => {
      inFlight++
      if (inFlight > 1) concurrentCalls++
      await new Promise((r) => setTimeout(r, 20))
      inFlight--
      return {}
    })

    const first = watcher.refreshProviders()
    const second = watcher.refreshProviders()
    await Promise.all([first, second])

    expect(concurrentCalls).toBe(0)
  })

  it("does not start the second query until the first has finished", async () => {
    const events: string[] = []
    let releaseFirst!: () => void
    const { watcher } = makeWatcher(async () => {
      const id = events.filter((e) => e.startsWith("start")).length + 1
      events.push(`start-${id}`)
      if (id === 1) {
        await new Promise<void>((resolve) => {
          releaseFirst = resolve
        })
      }
      events.push(`end-${id}`)
      return {}
    })

    const first = watcher.refreshProviders()
    await Promise.resolve() // let the first query actually start
    const second = watcher.refreshProviders()
    await Promise.resolve()
    releaseFirst()
    await Promise.all([first, second])

    expect(events).toEqual(["start-1", "end-1", "start-2", "end-2"])
  })
})

describe("Watcher.refreshWorkspaceStatuses", () => {
  it("limits status queries to six concurrent workers", async () => {
    const workspaces = Array.from({ length: 12 }, (_, i) => ({
      id: `ws-${i}`,
    }))
    let inFlight = 0
    let maxInFlight = 0
    const { watcher } = makeWatcher(
      async () => ({}),
      workspaces,
      async () => {
        inFlight++
        maxInFlight = Math.max(maxInFlight, inFlight)
        await new Promise((resolve) => setTimeout(resolve, 5))
        inFlight--
        return JSON.stringify({ state: "running" })
      },
    )

    await watcher.refreshWorkspaceStatuses()

    expect(maxInFlight).toBe(6)
  })

  it("serializes explicit refreshes and preserves status on failure", async () => {
    const workspaces = [{ id: "ws-1", status: "running" }]
    let inFlight = 0
    let maxInFlight = 0
    let calls = 0
    const { watcher } = makeWatcher(
      async () => ({}),
      workspaces,
      async () => {
        calls++
        inFlight++
        maxInFlight = Math.max(maxInFlight, inFlight)
        await new Promise((resolve) => setTimeout(resolve, 5))
        inFlight--
        if (calls === 1) throw new Error("offline")
        return JSON.stringify({ state: "stopped" })
      },
    )

    await Promise.all([
      watcher.refreshWorkspaceStatuses(),
      watcher.refreshWorkspaceStatus("ws-1"),
    ])

    expect(maxInFlight).toBe(1)
    expect(workspaces[0].status).toBe("stopped")
  })

  it("batches one broadcast for multiple changed statuses", async () => {
    const workspaces = [
      { id: "ws-1", status: "running" },
      { id: "ws-2", status: "busy" },
    ]
    const { watcher } = makeWatcher(
      async () => ({}),
      workspaces,
      async () => JSON.stringify({ state: "stopped" }),
    )
    const broadcast = vi.spyOn(watcher, "broadcastWorkspaces")

    await watcher.refreshWorkspaceStatuses()

    expect(broadcast).toHaveBeenCalledTimes(1)
  })

  it("continues polling after a failed query", async () => {
    const workspaces = [{ id: "ws-1", status: "running" }]
    let calls = 0
    const { watcher } = makeWatcher(
      async () => ({}),
      workspaces,
      async () => {
        calls++
        if (calls === 1) throw new Error("offline")
        return JSON.stringify({ state: "stopped" })
      },
    )

    await watcher.refreshWorkspaceStatuses()
    expect(workspaces[0].status).toBe("running")
    await watcher.refreshWorkspaceStatuses()
    expect(workspaces[0].status).toBe("stopped")
  })

  it("supports targeted refreshes", async () => {
    const workspaces = [
      { id: "ws-1", status: "running" },
      { id: "ws-2", status: "running" },
    ]
    const queried: string[] = []
    const { watcher } = makeWatcher(
      async () => ({}),
      workspaces,
      async (args) => {
        queried.push(args[2])
        return JSON.stringify({ state: "stopped" })
      },
    )

    await watcher.refreshWorkspaceStatus("ws-2")

    expect(queried).toEqual(["ws-2"])
    expect(workspaces[0].status).toBe("running")
    expect(workspaces[1].status).toBe("stopped")
  })
})
