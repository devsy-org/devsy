import { describe, expect, it, vi } from "vitest"
import { Watcher } from "../watcher.js"

function makeWatcher(runProviderList: () => Promise<Record<string, unknown>>) {
  const state = {
    updateProviders: vi.fn().mockReturnValue(false),
    providerList: vi.fn().mockReturnValue([]),
  }
  const cli = {
    run: vi.fn((args: string[]) => {
      if (args[0] === "provider" && args[1] === "list") {
        return runProviderList()
      }
      return Promise.resolve([])
    }),
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
    // Simulates the race the fix closes: a manual refresh (e.g. after an
    // install finishes) landing while a scheduled poll's provider query is
    // still in flight. Both queries hitting the CLI at once could let the
    // scheduled poll's stale result overwrite the refresh's fresh one.
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
    // Counting completed queries isn't enough to prove ordering: two
    // concurrent queries and two serialized ones both finish two queries.
    // Recording start/end order is what actually distinguishes them.
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
