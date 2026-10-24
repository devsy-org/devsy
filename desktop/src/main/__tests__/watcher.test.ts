import { describe, expect, it, vi } from "vitest"
import { DaemonState } from "../state.js"
import { parseProviderEntries, Watcher } from "../watcher.js"
import { WorkspaceJobs } from "../workspace-jobs.js"

function makeWatcher(
  runProviderList: () => Promise<Record<string, unknown>>,
  workspaces: Array<{ id: string; status?: string }> = [],
  runRaw: (args: string[]) => Promise<unknown> = async () => ({}),
) {
  const state = {
    updateProviders: vi.fn().mockReturnValue(false),
    updateWorkspaces: vi.fn().mockReturnValue(false),
    providerReadRevision: vi.fn().mockReturnValue(0),
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
  const workspaceJobs = new WorkspaceJobs()
  const watcher = new Watcher({
    cli: cli as never,
    state: state as never,
    getMainWindow: () => null,
    providerJobs: providerJobs as never,
    workspaceJobs: workspaceJobs as never,
  })
  return { watcher, cli, state, workspaceJobs }
}

describe("parseProviderEntries", () => {
  it("maps the authoritative deprecation flag and defaults it for older CLI output", () => {
    const [deprecated, legacy, external] = parseProviderEntries({
      deprecated: {
        config: { name: "custom-builtin-name" },
        deprecated: true,
      },
      legacy: { config: { name: "legacy-provider" } },
      external: {
        config: {
          name: "microsandbox",
          source: { github: "example/provider" },
        },
        deprecated: false,
      },
    })

    expect(deprecated).toMatchObject({
      name: "custom-builtin-name",
      deprecated: true,
    })
    expect(legacy).toMatchObject({ name: "legacy-provider", deprecated: false })
    expect(external).toMatchObject({ name: "microsandbox", deprecated: false })
  })
})

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

  it("discards a provider snapshot read before a confirmed mode write", async () => {
    let release!: (value: Record<string, unknown>) => void
    const state = new DaemonState()
    state.updateProviders([
      { name: "docker", state: { initialized: true, singleMachine: true } },
    ])
    const cli = {
      run: vi.fn(
        () =>
          new Promise<Record<string, unknown>>((resolve) => {
            release = resolve
          }),
      ),
    }
    const watcher = new Watcher({
      cli: cli as never,
      state,
      getMainWindow: () => null,
      providerJobs: { snapshot: () => ({}) } as never,
      workspaceJobs: new WorkspaceJobs(),
    })

    const polling = watcher.refreshProviders()
    await vi.waitFor(() => expect(cli.run).toHaveBeenCalledOnce())
    state.confirmProviderSingleMachine("docker", false)
    release({
      docker: {
        config: { name: "docker" },
        state: { initialized: true, singleMachine: true },
      },
    })
    await polling

    expect(state.providerList()[0].state?.singleMachine).toBe(false)
  })

  it("reads providers from the CLI after a mode write instead of the daemon cache", async () => {
    const state = new DaemonState()
    state.updateProviders([
      { name: "docker", state: { initialized: true, singleMachine: false } },
    ])
    state.confirmProviderSingleMachine("docker", false)
    const daemon = {
      listProviders: vi.fn(async () => ({
        docker: {
          config: { name: "docker" },
          state: { initialized: true, singleMachine: false },
        },
      })),
    }
    const cli = {
      run: vi.fn(async () => ({
        docker: {
          config: { name: "docker" },
          state: { initialized: true, singleMachine: true },
        },
      })),
    }
    const watcher = new Watcher({
      cli: cli as never,
      daemon: daemon as never,
      state,
      getMainWindow: () => null,
      providerJobs: { snapshot: () => ({}) } as never,
      workspaceJobs: new WorkspaceJobs(),
    })

    await watcher.refreshProviders()

    expect(daemon.listProviders).not.toHaveBeenCalled()
    expect(cli.run).toHaveBeenCalledExactlyOnceWith(["provider", "list"])
    expect(state.providerList()[0].state?.singleMachine).toBe(true)
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

describe("workspace status ordering", () => {
  it("discards an observation started before an operation", async () => {
    let release!: (value: string) => void
    const { watcher, state, workspaceJobs } = makeWatcher(
      async () => ({}),
      [{ id: "ws", status: "Running" }],
      () =>
        new Promise<string>((resolve) => {
          release = resolve
        }),
    )
    const polling = watcher.refreshWorkspaceStatuses()
    await Promise.resolve()
    workspaceJobs.start("ws", "stopping", "stop")
    release('{"state":"Running"}')
    await polling
    expect(state.updateWorkspaceStatus).not.toHaveBeenCalled()
  })
  it("propagates targeted refresh failure without poisoning subsequent refreshes", async () => {
    const { watcher, cli } = makeWatcher(
      async () => ({}),
      [{ id: "ws" }],
      async () => {
        throw new Error("offline")
      },
    )
    await expect(watcher.refreshWorkspaceStatus("ws")).rejects.toThrow(
      "offline",
    )
    cli.runRaw.mockResolvedValue('{"state":"Stopped"}')
    await expect(watcher.refreshWorkspaceStatus("ws")).resolves.toBeUndefined()
  })
})

describe("Watcher job broadcasts", () => {
  it.each(
    ["success", "failure"].flatMap((outcome) =>
      ["send", "lookup", "window", "contents"].map((boundary) => [
        outcome,
        boundary,
      ]),
    ),
  )(
    "keeps %s reconciliation independent of renderer %s failure",
    async (outcome, boundary) => {
      const state = new DaemonState()
      const workspaceJobs = new WorkspaceJobs()
      const send = vi.fn()
      const win = {
        isDestroyed: vi.fn(() => false),
        webContents: { isDestroyed: vi.fn(() => false), send },
      }
      const getMainWindow = vi.fn(() => win)
      const cli = {
        run: vi.fn(async () => [{ id: "ws", context: "default" }]),
        runRaw: vi.fn(async () => JSON.stringify({ state: "running" })),
      }
      const watcher = new Watcher({
        cli: cli as never,
        state,
        getMainWindow: getMainWindow as never,
        providerJobs: { snapshot: () => ({}) } as never,
        workspaceJobs,
      })
      workspaceJobs.onChange(() => watcher.broadcastWorkspaces())
      const refresh = vi.fn(async () => {
        await watcher.refreshWorkspaces()
        await watcher.refreshWorkspaceStatus("ws")
        watcher.broadcastWorkspaces()
      })
      workspaceJobs.setRefresh(refresh)
      const generation = workspaceJobs.start("ws", "creating", "first")
      send.mockClear()
      if (boundary === "send")
        send.mockImplementation(() => {
          throw new Error("renderer delivery failed")
        })
      if (boundary === "lookup")
        getMainWindow.mockImplementation(() => {
          throw new Error("window lookup failed")
        })
      if (boundary === "window") win.isDestroyed.mockReturnValue(true)
      if (boundary === "contents")
        win.webContents.isDestroyed.mockReturnValue(true)
      expect(() =>
        workspaceJobs.phase("ws", "first", "Launching command"),
      ).not.toThrow()
      expect(() =>
        workspaceJobs.progress("ws", "first", {
          phase: "building_image",
          state: "started",
        }),
      ).not.toThrow()
      const originalError = outcome === "failure" ? "invalid ID" : undefined
      await expect(
        workspaceJobs.finish("ws", generation, originalError),
      ).resolves.toBeUndefined()
      expect(refresh).toHaveBeenCalledOnce()
      expect(cli.run).toHaveBeenCalledExactlyOnceWith([
        "workspace",
        "list",
        "--skip-platform",
      ])
      expect(cli.runRaw).toHaveBeenCalledOnce()
      expect(workspaceJobs.get("ws")).toMatchObject({
        state: outcome === "failure" ? "failed" : "succeeded",
        error: originalError,
        refreshError: undefined,
      })
      if (boundary === "window" || boundary === "contents")
        expect(send).not.toHaveBeenCalled()
      expect(() => workspaceJobs.start("ws", "creating", "retry")).not.toThrow()
      expect(workspaceJobs.get("ws")).toMatchObject({
        commandId: "retry",
        state: "running",
      })
    },
  )
})

describe("Watcher log confirmation", () => {
  it.each([undefined, "command denied"])(
    "preserves log failures through polling and manual refresh (%s)",
    async (error) => {
      const { watcher, workspaceJobs } = makeWatcher(async () => ({}))
      const refresh = vi
        .fn()
        .mockRejectedValueOnce(new Error("offline"))
        .mockResolvedValue(undefined)
      workspaceJobs.setRefresh(refresh)
      const generation = workspaceJobs.start("ws", "creating", "first")
      await workspaceJobs.finish(
        "ws",
        generation,
        error,
        "Workspace log could not be saved: disk unavailable",
      )
      await (watcher as unknown as { pollOnce(): Promise<void> }).pollOnce()
      expect(refresh).not.toHaveBeenCalled()
      await workspaceJobs.retryRefresh("ws")
      expect(workspaceJobs.get("ws")?.refreshError).toBe("offline")
      await (watcher as unknown as { pollOnce(): Promise<void> }).pollOnce()
      for (let i = 0; i < 20; i++) await Promise.resolve()
      if (error) await workspaceJobs.retryRefresh("ws")
      expect(refresh).toHaveBeenCalledTimes(2)
      await workspaceJobs.retryRefresh("ws")
      expect(workspaceJobs.get("ws")).toMatchObject({
        state: error ? "failed" : "reconciling",
        logError: "Workspace log could not be saved: disk unavailable",
        refreshError: undefined,
        error,
      })
      const next = workspaceJobs.start("ws", "creating", "retry")
      expect(next).not.toBe(generation)
      expect(workspaceJobs.get("ws")?.state).toBe("running")
      expect(workspaceJobs.get("ws")?.logError).toBeUndefined()
      await workspaceJobs.finish("ws", generation)
      expect(workspaceJobs.get("ws")?.commandId).toBe("retry")
    },
  )
})
