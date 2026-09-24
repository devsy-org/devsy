// @vitest-environment node
import { EventEmitter } from "node:events"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { ProviderJobs } from "../provider-jobs.js"

const handlers = new Map<string, (...args: unknown[]) => unknown>()

vi.mock("electron", () => ({
  app: { getPath: () => "/tmp", getVersion: () => "0.0.0" },
  dialog: {},
  ipcMain: {
    handle: (channel: string, fn: (...args: unknown[]) => unknown) => {
      handlers.set(channel, fn)
    },
    on: () => undefined,
  },
}))

vi.mock("../analytics.js", () => ({
  hashWorkspaceRef: (v: string) => v,
  trackEvent: () => undefined,
}))

const { registerIpcHandlers } = await import("../ipc.js")

/** A child that emits the given stdout lines, then exits with `code`. */
function fakeChild(lines: string[], code: number) {
  const child = new EventEmitter() as EventEmitter & {
    exitCode: number | null
    signalCode: string | null
    kill: () => void
  }
  child.exitCode = null
  child.signalCode = null
  child.kill = () => undefined
  return { child, lines, code }
}

function setup(
  script: (cliArgs: string[]) => { lines: string[]; code: number } = () => ({
    lines: [],
    code: 0,
  }),
) {
  const providerJobs = new ProviderJobs()
  const cli = {
    run: vi.fn(async () => ({})),
    runRaw: vi.fn(async () => ""),
    runStreaming: vi.fn(
      async (
        cliArgs: string[],
        onLine: (line: string, stream: "stdout" | "stderr") => void,
        onExit: (code: number, cliError?: unknown) => void,
      ) => {
        const { lines, code } = script(cliArgs)
        const { child } = fakeChild(lines, code)
        // Deliver lines then exit asynchronously, as a real child does.
        setTimeout(() => {
          for (const line of lines) onLine(line, "stdout")
          onExit(code, code === 0 ? undefined : { message: "boom" })
        }, 0)
        return child
      },
    ),
    cancelFor: vi.fn(async () => undefined),
  }
  const send = vi.fn()
  const deps = {
    cli,
    state: { workspaceContext: () => "ctx", providerList: () => [] },
    logStore: {
      createLogFile: () => "/tmp/log.txt",
      appendLog: () => true,
      closeLog: async () => undefined,
      onDrain: async () => undefined,
    },
    pty: { cancelFor: vi.fn(async () => undefined) },
    getMainWindow: () => ({ webContents: { send } }),
    providerJobs,
  }
  // biome-ignore lint/suspicious/noExplicitAny: partial test doubles
  registerIpcHandlers(deps as any)
  return { providerJobs, cli, send }
}

function invoke(channel: string, args: Record<string, unknown>) {
  const handler = handlers.get(channel)
  if (!handler) throw new Error(`${channel} not registered`)
  return handler({}, args)
}

function statusLine(phase: string) {
  return JSON.stringify({ kind: "status", schemaVersion: 1, pipeline: "provider", phase, state: "started" })
}

describe("provider job lifecycle over IPC", () => {
  beforeEach(() => {
    handlers.clear()
    vi.clearAllMocks()
  })

  it("forwards managed environment attachment intent to the CLI", async () => {
    const { cli } = setup()
    await invoke("env_attach", { name: "LOG_LEVEL" })
    await invoke("env_detach", { name: "LOG_LEVEL" })
    expect(cli.runRaw).toHaveBeenCalledWith(["env", "attach", "LOG_LEVEL"])
    expect(cli.runRaw).toHaveBeenCalledWith(["env", "detach", "LOG_LEVEL"])
  })

  it("clears the job when init succeeds", async () => {
    const { providerJobs } = setup(() => ({
      lines: [statusLine("running_init"), statusLine("ready")],
      code: 0,
    }))

    await invoke("provider_init", { name: "docker" })

    expect(providerJobs.get("docker")).toBeUndefined()
  })

  it("records the failure when init fails", async () => {
    const { providerJobs } = setup(() => ({ lines: [], code: 1 }))

    const result = (await invoke("provider_init", { name: "docker" })) as {
      ok: boolean
    }

    expect(result.ok).toBe(false)
    expect(providerJobs.get("docker")?.error).toBeTruthy()
  })

  it("leaves the job open after add, for the chained init to close", async () => {
    // Closing it here would flash the red badge between add and init.
    const { providerJobs } = setup(() => ({
      lines: [statusLine("installing_provider")],
      code: 0,
    }))

    await invoke("provider_add", { name: "docker" })

    expect(providerJobs.get("docker")?.activity).toBe("installing")
  })

  it("releases an abandoned job so the card stops spinning", async () => {
    const { providerJobs } = setup(() => ({ lines: [], code: 0 }))

    await invoke("provider_add", { name: "docker" })
    expect(providerJobs.get("docker")).toBeDefined()

    await invoke("provider_release_job", { name: "docker" })

    expect(providerJobs.get("docker")).toBeUndefined()
  })

  it("keeps a failed job's error when released", async () => {
    const { providerJobs } = setup(() => ({ lines: [], code: 1 }))

    await invoke("provider_init", { name: "docker" })
    await invoke("provider_release_job", { name: "docker" })

    expect(providerJobs.get("docker")?.error).toBeTruthy()
  })

  it("runs set-source then init on update, clearing the job once", async () => {
    const seen: string[][] = []
    const { providerJobs, send } = setup((cliArgs) => {
      seen.push(cliArgs)
      return { lines: [], code: 0 }
    })

    await invoke("provider_update", { name: "docker" })

    expect(seen.map((a) => a[1])).toEqual(["set-source", "init"])
    expect(providerJobs.get("docker")).toBeUndefined()
  })

  it("records the failure when the update's chained init fails", async () => {
    const { providerJobs } = setup((cliArgs) => ({
      lines: [],
      code: cliArgs[1] === "init" ? 1 : 0,
    }))

    await expect(
      invoke("provider_update", { name: "docker" }),
    ).rejects.toThrow()

    expect(providerJobs.get("docker")?.error).toBeTruthy()
  })


  it("streams update phases and completes the provider job", async () => {
    const seen: string[][] = []
    const { providerJobs, send } = setup((cliArgs) => {
      seen.push(cliArgs)
      return { lines: [statusLine(cliArgs[1] === "init" ? "running_init" : "downloading")], code: 0 }
    })

    const commandId = await invoke("provider_update_streaming", { name: "docker" })
    expect(commandId).toEqual(expect.any(String))
    expect(providerJobs.get("docker")?.activity).toBe("updating")

    await vi.waitFor(() => expect(providerJobs.get("docker")).toBeUndefined())
    expect(seen.map((args) => args[1])).toEqual(["set-source", "init"])
    expect(send).toHaveBeenCalledWith(
      "command-progress",
      expect.objectContaining({ commandId, success: true, done: true }),
    )
  })

  it("retains update failure details after a streaming update", async () => {
    const { providerJobs } = setup((cliArgs) => ({
      lines: [],
      code: cliArgs[1] === "init" ? 1 : 0,
    }))

    await invoke("provider_update_streaming", { name: "docker" })
    await vi.waitFor(() => expect(providerJobs.get("docker")?.error).toBeTruthy())
  })


  it("terminates a streaming update when provider refresh fails", async () => {
    const { providerJobs, send } = setup(() => ({ lines: [], code: 0 }))
    providerJobs.setRefresh(() => Promise.reject(new Error("refresh boom")))

    const commandId = await invoke("provider_update_streaming", { name: "docker" })

    await vi.waitFor(() =>
      expect(providerJobs.get("docker")?.errorCode).toBe("provider_refresh_failed"),
    )
    expect(send).toHaveBeenCalledWith(
      "command-progress",
      expect.objectContaining({ commandId, success: false, done: true }),
    )
  })


  it("does not blame a successful init for a refresh failure afterward", async () => {
    const { providerJobs } = setup(() => ({
      lines: [statusLine("running_init"), statusLine("ready")],
      code: 0,
    }))
    providerJobs.setRefresh(() => Promise.reject(new Error("refresh boom")))

    await invoke("provider_init", { name: "docker" })

    expect(providerJobs.get("docker")?.error).not.toBe("refresh boom")
  })

  it("refreshes and clears a retained provider refresh failure", async () => {
    const { providerJobs } = setup(() => ({ lines: [], code: 0 }))
    const refresh = vi.fn().mockResolvedValue(undefined)
    providerJobs.setRefresh(refresh)
    providerJobs.start("docker", "updating")
    await providerJobs.finish("docker", {
      code: "provider_refresh_failed",
      message: "status unavailable",
    })

    const result = await invoke("provider_refresh_state", { name: "docker" })

    expect(result).toEqual({ ok: true })
    expect(refresh).toHaveBeenCalledOnce()
    expect(providerJobs.get("docker")).toBeUndefined()
  })

  it("returns failure and retains recovery when provider refresh still fails", async () => {
    const { providerJobs } = setup(() => ({ lines: [], code: 0 }))
    providerJobs.setRefresh(() => Promise.reject(new Error("still unavailable")))
    providerJobs.start("docker", "updating")
    await providerJobs.finish("docker", {
      code: "provider_refresh_failed",
      message: "status unavailable",
    })

    const result = await invoke("provider_refresh_state", { name: "docker" })

    expect(result).toEqual({ ok: false, message: "still unavailable" })
    expect(providerJobs.get("docker")?.errorCode).toBe("provider_refresh_failed")
  })

})
