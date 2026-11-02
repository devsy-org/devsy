import { describe, expect, it, vi } from "vitest"
import type {
  LogReadResponse,
  LogRecord,
  LogRunSummary,
} from "$shared/workspace-logs.js"
import {
  MAX_PAGES_PER_LOAD,
  RunReader,
  type RunReaderState,
  resolveFocus,
} from "./workspace-run-reader.js"

const RUN = "11111111-1111-4111-8111-111111111111"
const PROD = "b0000000-0000-4000-8000-000000000001"
const INV = "a1111111-0000-4000-8000-000000000001"

const summary = (patch: Partial<LogRunSummary> = {}): LogRunSummary => ({
  runId: RUN,
  context: "default",
  workspaceUid: "ws-1",
  command: "up",
  createdAt: "2026-10-30T00:00:00Z",
  commandState: "succeeded",
  captureState: "complete",
  ...patch,
})

const record = (
  sequence: number,
  patch: Partial<LogRecord> = {},
): LogRecord => ({
  schemaVersion: 1,
  kind: "output",
  runId: RUN,
  producerId: PROD,
  sequence,
  time: "t",
  invocationId: INV,
  stream: "stdout",
  text: `line ${sequence}\n`,
  ...patch,
})

const page = (
  records: LogRecord[],
  patch: Partial<LogReadResponse> = {},
): LogReadResponse => ({
  schemaVersion: 1,
  run: summary(),
  records,
  hasMore: false,
  complete: true,
  nextCursor: "next",
  ...patch,
})

function harness(read: (req: unknown) => Promise<LogReadResponse | null>) {
  const states: RunReaderState[] = []
  const timers: Array<{ fn: () => void; ms: number }> = []
  const reader = new RunReader({
    read: read as never,
    onChange: (s) => states.push(s),
    setTimer: (fn, ms) => {
      timers.push({ fn, ms })
      return timers.length
    },
    clearTimer: () => undefined,
  })
  return { reader, states, timers }
}

const settle = () => new Promise((r) => setTimeout(r, 0))
const request = { context: "default", runId: RUN }

describe("RunReader", () => {
  it("pages until the response says nothing is left", async () => {
    const pages = [
      page([record(1)], { hasMore: true, nextCursor: "c1" }),
      page([record(2)], { nextCursor: "c2" }),
    ]
    const read = vi.fn(async () => pages.shift() ?? null)
    const { reader } = harness(read)
    reader.start(request)
    await settle()
    expect(read).toHaveBeenCalledTimes(2)
    expect((read.mock.calls as unknown[][])[1][0]).toMatchObject({
      after: "c1",
      limit: 500,
    })
    expect(reader.snapshot().records.map((r) => r.sequence)).toEqual([1, 2])
    expect(reader.snapshot().loading).toBe(false)
  })

  it("drops a late answer for a previous selection", async () => {
    const resolvers: Array<(r: LogReadResponse) => void> = []
    const read = vi.fn(
      () => new Promise<LogReadResponse | null>((res) => resolvers.push(res)),
    )
    const { reader } = harness(read)
    reader.start(request)
    reader.start({ ...request, context: "other" })
    resolvers[0](page([record(1, { text: "stale\n" })]))
    resolvers[1](page([record(1, { text: "fresh\n" })]))
    await settle()
    expect(reader.snapshot().records.map((r) => r.text)).toEqual(["fresh\n"])
  })

  it("keeps one read in flight while polling is pending", async () => {
    let calls = 0
    const read = vi.fn(async () => {
      calls++
      return page([], {
        complete: false,
        run: summary({ commandState: "running" }),
      })
    })
    const { reader, timers } = harness(read)
    reader.start(request)
    await settle()
    expect(calls).toBe(1)
    expect(timers).toHaveLength(1)
    timers[0].fn()
    await settle()
    expect(calls).toBe(2)
  })

  it("deduplicates by producer and sequence on retry", async () => {
    const pages = [
      page([record(1), record(2)], {
        complete: false,
        run: summary({ commandState: "running" }),
      }),
      page([record(2), record(3)]),
    ]
    const read = vi.fn(async () => pages.shift() ?? null)
    const { reader, timers } = harness(read)
    reader.start(request)
    await settle()
    timers[0].fn()
    await settle()
    expect(reader.snapshot().records.map((r) => r.sequence)).toEqual([1, 2, 3])
  })

  it("stops polling once the run is complete or a source is unavailable", async () => {
    const done = harness(async () => page([record(1)]))
    done.reader.start(request)
    await settle()
    expect(done.timers).toHaveLength(0)
    const offline = harness(async () =>
      page([], {
        complete: false,
        run: summary({ commandState: "running" }),
        availability: [{ producerId: PROD, available: false }],
      }),
    )
    offline.reader.start(request)
    await settle()
    expect(offline.timers).toHaveLength(0)
  })

  it("keeps polling while another source can still produce records", async () => {
    const mixed = harness(async () =>
      page([], {
        complete: false,
        run: summary({ commandState: "running" }),
        availability: [
          { producerId: PROD, available: false },
          {
            producerId: "b0000000-0000-4000-8000-000000000002",
            available: true,
          },
        ],
      }),
    )
    mixed.reader.start(request)
    await settle()
    expect(mixed.timers).toHaveLength(1)
  })

  it("treats a null read as superseded, not as an error", async () => {
    const { reader } = harness(async () => null)
    reader.start(request)
    await settle()
    expect(reader.snapshot()).toMatchObject({ loading: false, records: [] })
    expect(reader.snapshot().error).toBeUndefined()
  })

  it("surfaces read errors for the current selection only", async () => {
    const { reader } = harness(async () => {
      throw new Error("cli unavailable")
    })
    reader.start(request)
    await settle()
    expect(reader.snapshot().error).toBe("cli unavailable")
  })

  it("bounds the pages loaded per view", async () => {
    const read = vi.fn(async () =>
      page([record(read.mock.calls.length)], {
        hasMore: true,
        nextCursor: "n",
      }),
    )
    const { reader } = harness(read)
    reader.start(request)
    await settle()
    expect(read).toHaveBeenCalledTimes(MAX_PAGES_PER_LOAD)
    expect(reader.snapshot().viewTruncated).toBe(true)
  })
})

describe("resolveFocus", () => {
  const views = [
    { id: "a", state: "succeeded" },
    { id: "b", state: "failed" },
  ]
  it("opens the pinned invocation when it exists", () => {
    expect(resolveFocus(views, "b", undefined, true)).toEqual({
      mode: "invocation",
      id: "b",
    })
  })
  it("falls back to failed invocations when the pinned one is missing", () => {
    expect(resolveFocus(views, "zzz", undefined, true)).toEqual({
      mode: "failed",
      fallback: true,
    })
  })
  it("shows failed steps for a run link without an invocation", () => {
    expect(resolveFocus(views, undefined, undefined, true)).toEqual({
      mode: "failed",
      fallback: false,
    })
  })
  it("lets an explicit step choice win over the link", () => {
    expect(resolveFocus(views, "b", "a", true)).toEqual({
      mode: "invocation",
      id: "a",
    })
  })
  it("shows everything when nothing is pinned", () => {
    expect(resolveFocus(views, undefined, undefined, false)).toEqual({
      mode: "all",
    })
  })
})
