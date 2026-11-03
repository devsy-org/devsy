// @vitest-environment node
import { mkdtemp, readdir, readFile, rm } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import {
  MOCK_RUN_FAILED,
  MOCK_RUN_OK,
  MOCK_RUN_PARTIAL,
  mockLogRunRead,
  mockLogRuns,
} from "../../renderer/src/lib/ipc/mock.js"
import type { LogReadResponse } from "../../shared/workspace-logs.js"
import {
  MAX_RESPONSE_BYTES,
  WorkspaceLogError,
  WorkspaceLogReader,
} from "../workspace-logs.js"

type Run = (
  args: string[],
  options?: { maxBuffer?: number; signal?: AbortSignal },
) => Promise<unknown>

function reader(run: Run, timeoutMs?: number) {
  const cli = { run: vi.fn(run) as never }
  return {
    reader: new WorkspaceLogReader(cli, { timeoutMs }),
    cli: cli as { run: ReturnType<typeof vi.fn> },
  }
}

const readArgs = { context: "default", runId: MOCK_RUN_FAILED }

describe("WorkspaceLogReader", () => {
  it("builds the setup-source command with a byte cap", async () => {
    const { reader: r, cli } = reader(async () =>
      mockLogRunRead({ runId: MOCK_RUN_FAILED }),
    )
    await r.read({
      ...readArgs,
      workspaceId: "my-project",
      hook: "postCreateCommand",
      after: "abc",
      limit: 100,
    })
    expect(cli.run).toHaveBeenCalledWith(
      [
        "workspace",
        "logs",
        "my-project",
        "--source",
        "setup",
        "--run",
        MOCK_RUN_FAILED,
        "--context",
        "default",
        "--hook",
        "postCreateCommand",
        "--limit",
        "100",
        "--after",
        "abc",
        "--result-format",
        "json",
      ],
      { maxBuffer: MAX_RESPONSE_BYTES, signal: expect.any(AbortSignal) },
    )
  })

  it("lists runs for a context without a workspace argument", async () => {
    const { reader: r, cli } = reader(async () =>
      mockLogRuns({ context: "default" }),
    )
    const res = await r.runs({ context: "default" })
    expect(res.runs).toHaveLength(4)
    expect(cli.run.mock.calls[0][0]).toEqual([
      "workspace",
      "logs",
      "--source",
      "setup",
      "--list",
      "--context",
      "default",
      "--result-format",
      "json",
    ])
  })

  it("rejects invalid requests before running the CLI", async () => {
    const { reader: r, cli } = reader(async () => ({}))
    await expect(
      r.read({ context: "default", runId: "latest" }),
    ).rejects.toMatchObject({ code: "contract" })
    await expect(r.runs({ context: "-x" })).rejects.toBeInstanceOf(
      WorkspaceLogError,
    )
    expect(cli.run).not.toHaveBeenCalled()
  })

  it("rejects a response for another run, context or workspace", async () => {
    const page = mockLogRunRead({ runId: MOCK_RUN_FAILED })
    const wrongRun = reader(async () => mockLogRunRead({ runId: MOCK_RUN_OK }))
    await expect(wrongRun.reader.read(readArgs)).rejects.toThrow(
      "different run",
    )
    const wrongCtx = reader(async () => ({
      ...page,
      run: { ...page.run, context: "other" },
    }))
    await expect(wrongCtx.reader.read(readArgs)).rejects.toThrow(
      "different context",
    )
    const wrongUid = reader(async () => page)
    await expect(
      wrongUid.reader.read({ ...readArgs, workspaceUid: "ws-999" }),
    ).rejects.toThrow("different workspace")
  })

  it("supersedes an in-flight read so only one stays active", async () => {
    let calls = 0
    const releases: Array<() => void> = []
    const { reader: r } = reader(
      () =>
        new Promise((resolve) => {
          calls++
          releases.push(() =>
            resolve(mockLogRunRead({ runId: MOCK_RUN_FAILED })),
          )
        }),
    )
    const first = r.read(readArgs)
    const second = r.read(readArgs)
    await expect(first).rejects.toMatchObject({ code: "cancelled" })
    for (const release of releases) release()
    await expect(second).resolves.toBeDefined()
    expect(calls).toBe(2)
  })

  it("cancels only readers, never other CLI work", async () => {
    const cancelFor = vi.fn()
    const { reader: r } = reader(() => new Promise(() => undefined))
    const pending = r.read(readArgs)
    r.cancel()
    await expect(pending).rejects.toMatchObject({ code: "cancelled" })
    expect(cancelFor).not.toHaveBeenCalled()
  })

  it("times out a stuck read", async () => {
    vi.useFakeTimers()
    try {
      const { reader: r } = reader(() => new Promise(() => undefined), 1000)
      const pending = r.read(readArgs)
      const assertion = expect(pending).rejects.toMatchObject({
        code: "timeout",
      })
      await vi.advanceTimersByTimeAsync(1001)
      await assertion
    } finally {
      vi.useRealTimers()
    }
  })

  it("aborts the CLI child when a read is cancelled or times out", async () => {
    const signals: (AbortSignal | undefined)[] = []
    const stuck: Run = (_args, options) => {
      signals.push(options?.signal)
      return new Promise(() => undefined)
    }
    const { reader: r } = reader(stuck)
    const cancelled = r.read(readArgs)
    r.cancel()
    await expect(cancelled).rejects.toMatchObject({ code: "cancelled" })
    expect(signals[0]?.aborted).toBe(true)

    vi.useFakeTimers()
    try {
      const { reader: timed } = reader(stuck, 1000)
      const pending = timed.read(readArgs)
      const assertion = expect(pending).rejects.toMatchObject({
        code: "timeout",
      })
      await vi.advanceTimersByTimeAsync(1001)
      await assertion
      expect(signals[1]?.aborted).toBe(true)
    } finally {
      vi.useRealTimers()
    }
  })

  it("wraps CLI failures", async () => {
    const { reader: r } = reader(async () => {
      throw new Error("exit 1")
    })
    await expect(r.read(readArgs)).rejects.toMatchObject({
      code: "cli",
      message: "exit 1",
    })
  })
})

describe("export", () => {
  let dir: string
  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), "devsy-export-"))
  })
  afterEach(async () => {
    await rm(dir, { recursive: true, force: true })
  })

  function pagedRun(): Run {
    const full = mockLogRunRead({ runId: MOCK_RUN_FAILED })
    const pages: LogReadResponse[] = [
      {
        ...full,
        records: full.records.slice(0, 3),
        hasMore: true,
        nextCursor: "c1",
      },
      {
        ...full,
        records: full.records.slice(3),
        hasMore: false,
        nextCursor: "c2",
      },
    ]
    return async (args) => {
      const after = args.indexOf("--after")
      return after === -1 ? pages[0] : pages[1]
    }
  }

  it("writes bounded pages to a temp file and renames on success", async () => {
    const { reader: r, cli } = reader(pagedRun())
    const dest = join(dir, "out.txt")
    const result = await r.exportRun(readArgs, dest)
    expect(cli.run).toHaveBeenCalledTimes(2)
    expect(await readdir(dir)).toEqual(["out.txt"])
    const text = await readFile(dest, "utf8")
    expect(text).toContain("npm install")
    // Output stays literal even when it looks like JSON or HTML.
    expect(text).toContain('{"level":"error","msg":"not a log line"}')
    expect(text).toContain("<b>literal</b>")
    expect(text).toContain("[postCreateCommand [Running]][out] npm install")
    expect(result.gaps).toBe(false)
    expect(result.bytes).toBeGreaterThan(0)
  })

  it("states gaps in the saved file for partial history", async () => {
    const { reader: r } = reader(async () =>
      mockLogRunRead({ runId: MOCK_RUN_PARTIAL }),
    )
    const dest = join(dir, "partial.txt")
    const result = await r.exportRun(
      { context: "default", runId: MOCK_RUN_PARTIAL },
      dest,
    )
    expect(result.gaps).toBe(true)
    const text = await readFile(dest, "utf8")
    expect(text).toContain("# Capture condition: source unavailable")
    expect(text).toContain("# History is incomplete")
  })

  it("removes the partial file when a page fails", async () => {
    let n = 0
    const { reader: r } = reader(async (args) => {
      if (n++ === 1) throw new Error("boom")
      return pagedRun()(args)
    })
    await expect(r.exportRun(readArgs, join(dir, "out.txt"))).rejects.toThrow(
      "boom",
    )
    expect(await readdir(dir)).toEqual([])
  })

  it("treats cancellation as a clean stop and removes the partial file", async () => {
    let release!: () => void
    const { reader: r } = reader(
      (args) =>
        new Promise((resolve) => {
          if (args.includes("--after")) return
          release = () => resolve(pagedRun()(args))
        }),
    )
    const pending = r.exportRun(readArgs, join(dir, "out.txt"))
    await vi.waitFor(() => expect(release).toBeDefined())
    r.cancel()
    await expect(pending).rejects.toMatchObject({ code: "cancelled" })
    expect(await readdir(dir)).toEqual([])
  })

  it("cancels an export that is waiting on a later page", async () => {
    let started = false
    const { reader: r } = reader(
      (args) =>
        new Promise((resolve) => {
          if (args.includes("--after")) {
            started = true
            return
          }
          resolve(pagedRun()(args))
        }),
    )
    const pending = r.exportRun(readArgs, join(dir, "out.txt"))
    await vi.waitFor(() => expect(started).toBe(true))
    r.cancel()
    await expect(pending).rejects.toMatchObject({ code: "cancelled" })
    expect(await readdir(dir)).toEqual([])
  })

  it("fails when paging does not advance", async () => {
    const full = mockLogRunRead({ runId: MOCK_RUN_FAILED })
    const { reader: r } = reader(async () => ({
      ...full,
      hasMore: true,
      nextCursor: undefined,
    }))
    await expect(r.exportRun(readArgs, join(dir, "out.txt"))).rejects.toThrow(
      "did not advance",
    )
    expect(await readdir(dir)).toEqual([])
  })
})
