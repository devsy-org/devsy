import { describe, expect, it } from "vitest"
import {
  MOCK_INV_POST_CREATE_OK,
  MOCK_INV_SILENT_OK,
  MOCK_INV_UNCAPTURED,
  MOCK_RUN_FAILED,
  MOCK_RUN_OK,
  MOCK_RUN_PARTIAL,
  mockLogRunRead,
  mockLogRuns,
} from "../../renderer/src/lib/ipc/mock.js"
import {
  runIdentityFromContext,
  runIdFromPayload,
} from "../../shared/cli-error.js"
import {
  InvocationTable,
  type LogRecord,
  outputPresence,
  parseIndexResponse,
  parseReadResponse,
  TranscriptBuilder,
  validateRunExportRequest,
  validateRunReadRequest,
  validateRunsRequest,
  WorkspaceLogContractError,
} from "../../shared/workspace-logs.js"

const RUN = "11111111-1111-4111-8111-111111111111"
const INV = "a1111111-0000-4000-8000-000000000001"
const PROD = "b0000000-0000-4000-8000-000000000001"

function output(sequence: number, text: string, stream = "stdout"): LogRecord {
  return {
    schemaVersion: 1,
    kind: "output",
    runId: RUN,
    producerId: PROD,
    sequence,
    time: "2026-10-30T00:00:00Z",
    invocationId: INV,
    stream: stream as LogRecord["stream"],
    text,
  }
}

describe("run identity parsing", () => {
  it("accepts string-valued run context and drops anything else", () => {
    expect(
      runIdentityFromContext({ runId: RUN, failedInvocationId: INV }),
    ).toEqual({ runId: RUN, failedInvocationId: INV })
    expect(runIdentityFromContext({ runId: "op-1" })).toEqual({})
    expect(runIdentityFromContext({ failedInvocationId: INV })).toEqual({})
    expect(runIdentityFromContext(undefined)).toEqual({})
  })

  it("reads an optional run ID from result and submission payloads", () => {
    expect(runIdFromPayload({ kind: "result", runId: RUN })).toBe(RUN)
    expect(runIdFromPayload({ kind: "task", id: "t" })).toBeUndefined()
    expect(runIdFromPayload({ runId: 7 })).toBeUndefined()
    expect(runIdFromPayload(null)).toBeUndefined()
  })
})

describe("request validation", () => {
  it("rejects selectors that could reach the command line as flags", () => {
    expect(() => validateRunsRequest({ context: "--help" })).toThrow(
      WorkspaceLogContractError,
    )
    expect(() =>
      validateRunReadRequest({ context: "default", runId: "latest" }),
    ).toThrow("runId")
    expect(() =>
      validateRunReadRequest({ context: "default", runId: RUN, hook: "x" }),
    ).toThrow("hook")
    expect(() =>
      validateRunReadRequest({
        context: "default",
        runId: RUN,
        after: "../../etc",
      }),
    ).toThrow("cursor")
    expect(() =>
      validateRunReadRequest({ context: "default", runId: RUN, limit: 5000 }),
    ).toThrow("limit")
  })

  it("accepts a bounded, well-formed request", () => {
    expect(
      validateRunReadRequest({
        context: "default",
        runId: RUN,
        hook: "postCreateCommand",
        after: "abc_-=",
        limit: 500,
      }),
    ).toMatchObject({ hook: "postCreateCommand", limit: 500 })
    expect(() =>
      validateRunExportRequest({
        context: "default",
        runId: RUN,
        hook: "postCreateCommand",
        invocationId: INV,
      }),
    ).toThrow("cannot be combined")
  })
})

describe("response parsing", () => {
  it("parses mock fixtures that mirror the Go contract", () => {
    const res = parseReadResponse(mockLogRunRead({ runId: MOCK_RUN_FAILED }))
    expect(res.records.length).toBeGreaterThan(0)
    expect(
      parseIndexResponse(mockLogRuns({ context: "default" })).runs,
    ).toHaveLength(4)
  })

  it("rejects wrong schema versions, null records and foreign records", () => {
    const good = mockLogRunRead({ runId: MOCK_RUN_FAILED })
    expect(() => parseReadResponse({ ...good, schemaVersion: 2 })).toThrow(
      "schema",
    )
    expect(() => parseReadResponse({ ...good, records: null })).toThrow("array")
    const foreign = { ...good.records[0], runId: MOCK_RUN_OK }
    expect(() => parseReadResponse({ ...good, records: [foreign] })).toThrow(
      "different run",
    )
    expect(() =>
      parseReadResponse({
        ...good,
        records: [{ ...good.records[0], kind: "bogus" }],
      }),
    ).toThrow("record kind")
  })
})

function view<T>(map: Map<string, T>, id: string): T {
  const found = map.get(id)
  if (!found) throw new Error(`missing invocation ${id}`)
  return found
}

describe("invocation table", () => {
  const views = (runId: string) => {
    const table = new InvocationTable()
    const res = mockLogRunRead({ runId })
    table.apply(res.records)
    table.markUnavailable(
      (res.availability ?? [])
        .filter((a) => !a.available)
        .map((a) => a.producerId),
    )
    return new Map(table.list().map((v) => [v.id, v]))
  }

  it("separates zero-byte success from output never captured", () => {
    const ok = views(MOCK_RUN_OK)
    expect(outputPresence(view(ok, MOCK_INV_SILENT_OK))).toBe("empty")
    expect(outputPresence(view(ok, MOCK_INV_POST_CREATE_OK))).toBe("has-output")
    const partial = views(MOCK_RUN_PARTIAL)
    expect(outputPresence(view(partial, MOCK_INV_UNCAPTURED))).toBe(
      "not-captured",
    )
  })

  it("keeps deferred, skipped and failed states with reasons", () => {
    const failed = [...views(MOCK_RUN_FAILED).values()]
    expect(failed.map((v) => v.state)).toEqual(["failed", "skipped"])
    expect(failed[0].exitCode).toBe(1)
    expect(failed[1].reason).toBe("already_attempted")
    const states = [...views(MOCK_RUN_OK).values()].map((v) => v.state)
    expect(states).toContain("deferred")
  })

  it("orders transitions by sequence, not arrival", () => {
    const table = new InvocationTable()
    const base = {
      schemaVersion: 1,
      kind: "invocation" as const,
      runId: RUN,
      producerId: PROD,
      time: "t",
      invocationId: INV,
      phase: "lifecycle",
      hook: "postCreateCommand",
    }
    table.apply([
      { ...base, sequence: 3, state: "succeeded", exitCode: 0 },
      { ...base, sequence: 2, state: "running" },
    ])
    expect(table.list()[0].state).toBe("succeeded")
  })

  it("keeps a worker result recorded at a lower sequence than the parent plan", () => {
    const table = new InvocationTable()
    const base = {
      schemaVersion: 1,
      kind: "invocation" as const,
      runId: RUN,
      invocationId: INV,
      phase: "lifecycle",
      hook: "postCreateCommand",
    }
    const worker = "b0000000-0000-4000-8000-000000000002"
    table.apply([
      {
        ...base,
        producerId: PROD,
        sequence: 9,
        time: "2026-01-01T00:00:01Z",
        state: "deferred",
      },
      {
        ...base,
        producerId: worker,
        sequence: 2,
        time: "2026-01-01T00:00:05Z",
        state: "failed",
        exitCode: 4,
      },
    ])
    const [view] = table.list()
    expect(view.state).toBe("failed")
    expect(view.exitCode).toBe(4)
  })
})

describe("transcript builder", () => {
  it("treats JSON, HTML and log-looking text as literal lines", () => {
    const text =
      '{"level":"error","msg":"x"}\n<b>bold</b>\n2026-01-01 INFO hi\n'
    const builder = new TranscriptBuilder()
    builder.append([output(1, text)])
    expect(builder.snapshot().map((l) => l.text)).toEqual([
      '{"level":"error","msg":"x"}',
      "<b>bold</b>",
      "2026-01-01 INFO hi",
    ])
  })

  it("preserves blank lines, indentation and a final fragment", () => {
    const builder = new TranscriptBuilder()
    builder.append([output(1, "a\n\n  b\npart")])
    builder.append([output(2, "ial")])
    expect(builder.snapshot().map((l) => l.text)).toEqual([
      "a",
      "",
      "  b",
      "partial",
    ])
  })

  it("keeps stream provenance and does not merge interleaved streams", () => {
    const builder = new TranscriptBuilder()
    builder.append([output(1, "out-", "stdout"), output(2, "err\n", "stderr")])
    builder.append([output(3, "rest\n", "stdout")])
    expect(builder.snapshot().map((l) => `${l.stream}:${l.text}`)).toEqual([
      "stdout:out-rest",
      "stderr:err",
    ])
  })
})
