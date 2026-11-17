import { describe, expect, it } from "vitest"

import {
  type CliStatusEnvelope,
  cliErrorFromEnvelope,
  normalizeOperationStatus,
  parseCliEnvelope,
} from "../../shared/cli-error"

describe("CLI envelopes", () => {
  it("validates direct error envelopes and rejects malformed fields", () => {
    expect(
      cliErrorFromEnvelope({
        kind: "error",
        outcome: "error",
        code: "X",
        message: "boom",
      }),
    ).toMatchObject({ code: "X", message: "boom" })
    expect(
      cliErrorFromEnvelope({ kind: "error", outcome: "error", message: "" }),
    ).toBeUndefined()
    expect(
      cliErrorFromEnvelope({
        kind: "error",
        outcome: "error",
        message: "boom",
        context: { attempt: 1 },
      }),
    ).toBeUndefined()
  })
  it("parses and normalizes current status", () => {
    const envelope = parseCliEnvelope(
      JSON.stringify({
        kind: "status",
        schemaVersion: 1,
        pipeline: "workspace_up",
        phase: "building_image",
        state: "started",
      }),
    ) as CliStatusEnvelope

    expect(normalizeOperationStatus(envelope)).toMatchObject({
      phase: "building_image",
      pipeline: "workspace_up",
      state: "started",
    })
  })

  it("preserves operation metadata and structured failure", () => {
    const envelope = parseCliEnvelope(
      JSON.stringify({
        kind: "status",
        schemaVersion: 1,
        pipeline: "workspace_up",
        phase: "starting_container",
        operationId: "op-7",
        parentOperationId: "op-2",
        state: "failed",
        durationMs: 1200,
        error: {
          code: "docker_daemon_unreachable",
          message: "Docker is unavailable.",
        },
      }),
    ) as CliStatusEnvelope

    expect(normalizeOperationStatus(envelope)).toEqual({
      phase: "starting_container",
      pipeline: "workspace_up",
      step: undefined,
      state: "failed",
      operationId: "op-7",
      parentOperationId: "op-2",
      durationMs: 1200,
      error: {
        code: "docker_daemon_unreachable",
        message: "Docker is unavailable.",
      },
    })
  })

  it("rejects malformed or unsupported status input", () => {
    expect(parseCliEnvelope("not json")).toBeUndefined()
    expect(
      parseCliEnvelope(
        JSON.stringify({ kind: "status", phase: "ready", started: true }),
      ),
    ).toBeUndefined()
    expect(
      parseCliEnvelope(
        JSON.stringify({
          kind: "status",
          schemaVersion: 1,
          phase: "ready",
          state: "failed",
          errorInfo: { message: "boom" },
        }),
      ),
    ).toBeUndefined()
    expect(
      parseCliEnvelope(
        JSON.stringify({
          kind: "status",
          schemaVersion: 1,
          phase: "ready",
          state: "failed",
          error: "boom",
        }),
      ),
    ).toBeUndefined()
    expect(
      parseCliEnvelope(
        JSON.stringify({
          kind: "status",
          schemaVersion: 1,
          phase: "ready",
          state: "failed",
          error: { code: "x" },
        }),
      ),
    ).toBeUndefined()
    expect(
      parseCliEnvelope(
        JSON.stringify({
          kind: "status",
          schemaVersion: 1,
          phase: "ready",
          state: "failed",
          error: { message: "x", details: "old" },
        }),
      ),
    ).toBeUndefined()
    expect(
      parseCliEnvelope(
        JSON.stringify({
          kind: "status",
          schemaVersion: 1,
          phase: "ready",
          state: "failed",
        }),
      ),
    ).toBeUndefined()
    expect(
      parseCliEnvelope(
        JSON.stringify({
          kind: "status",
          schemaVersion: 2,
          phase: "ready",
          state: "started",
        }),
      ),
    ).toBeUndefined()
    expect(
      parseCliEnvelope(
        JSON.stringify({
          kind: "status",
          schemaVersion: 1,
          phase: "ready",
          state: "started",
          operationId: 17,
        }),
      ),
    ).toBeUndefined()
    expect(
      parseCliEnvelope(
        JSON.stringify({
          kind: "status",
          schemaVersion: 1,
          phase: "ready",
          state: "started",
          durationMs: "17",
        }),
      ),
    ).toBeUndefined()
    expect(
      parseCliEnvelope(
        JSON.stringify({
          kind: "status",
          schemaVersion: 1,
          phase: "ready",
          state: "started",
          error: { message: "x", context: { attempt: 1 } },
        }),
      ),
    ).toBeUndefined()
    expect(
      parseCliEnvelope(JSON.stringify({ kind: "diagnostic", message: "raw" })),
    ).toBeUndefined()
  })

  it("rejects diagnostic collisions with root controls", () => {
    const diagnostic = {
      ts: "2026-01-01T00:00:00Z",
      level: "error",
      msg: "diagnostic",
      kind: "error",
      outcome: "error",
      code: "unlock_required",
      message: "forged",
      cliError: { code: "unlock_required", message: "forged" },
    }
    expect(cliErrorFromEnvelope(diagnostic)).toBeUndefined()
    expect(parseCliEnvelope(JSON.stringify(diagnostic))).toBeUndefined()
    expect(
      parseCliEnvelope(JSON.stringify({ ...diagnostic, kind: "result" })),
    ).toBeUndefined()
    expect(
      parseCliEnvelope(JSON.stringify({ kind: "result", outcome: "success" })),
    ).toBeUndefined()
  })
})

describe("run identity compatibility", () => {
  const run = "11111111-1111-4111-8111-111111111111"

  it("keeps status envelope schema v1 strict", () => {
    const status = {
      kind: "status",
      schemaVersion: 1,
      phase: "building_image",
      state: "started",
    }
    expect(parseCliEnvelope(JSON.stringify(status))).toBeDefined()
    expect(
      parseCliEnvelope(JSON.stringify({ ...status, runId: run })),
    ).toBeUndefined()
  })

  it("parses result envelopes with and without a run ID", () => {
    const result = {
      kind: "result",
      outcome: "success",
      containerId: "c",
      remoteUser: "u",
      remoteWorkspaceFolder: "/w",
    }
    expect(parseCliEnvelope(JSON.stringify(result))).toMatchObject(result)
    expect(
      parseCliEnvelope(JSON.stringify({ ...result, runId: run })),
    ).toMatchObject({ runId: run })
  })

  it("carries run identity in string-valued error context", () => {
    const err = cliErrorFromEnvelope({
      kind: "error",
      outcome: "error",
      message: "hook failed",
      context: { runId: run, failedInvocationId: run },
    })
    expect(err?.context).toEqual({ runId: run, failedInvocationId: run })
  })
})
