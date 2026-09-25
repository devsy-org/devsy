import { describe, expect, it } from "vitest"

import {
  cliErrorFromEnvelope,
  cliErrorFromLegacy,
  normalizeOperationStatus,
  parseCliEnvelope,
  type CliStatusEnvelope,
} from "../../shared/cli-error"

describe("CLI envelopes", () => {
	it("validates direct error envelopes and rejects malformed fields", () => {
    expect(cliErrorFromEnvelope({ kind: "error", outcome: "error", code: "X", message: "boom" })).toMatchObject({ code: "X", message: "boom" })
    expect(cliErrorFromEnvelope({ kind: "error", outcome: "error", message: "" })).toBeUndefined()
    expect(cliErrorFromEnvelope({ kind: "error", outcome: "error", message: "boom", context: { attempt: 1 } })).toBeUndefined()
  })
	it("parses and normalizes current status", () => {
		const envelope = parseCliEnvelope(
			JSON.stringify({ kind: "status", schemaVersion: 1, phase: "building_image", state: "started" }),
    ) as CliStatusEnvelope

    expect(normalizeOperationStatus(envelope)).toMatchObject({
      phase: "building_image",
      state: "started",
		})
  })

	it("preserves operation metadata and structured failure", () => {
    const envelope = parseCliEnvelope(
      JSON.stringify({
        kind: "status",
        schemaVersion: 1,
        phase: "starting_container",
        operationId: "op-7",
        parentOperationId: "op-2",
        state: "failed",
        durationMs: 1200,
        error: { code: "docker_daemon_unreachable", message: "Docker is unavailable." },
      }),
    ) as CliStatusEnvelope

    expect(normalizeOperationStatus(envelope)).toEqual({
      phase: "starting_container",
      step: undefined,
      state: "failed",
      operationId: "op-7",
      parentOperationId: "op-2",
      durationMs: 1200,
			error: { code: "docker_daemon_unreachable", message: "Docker is unavailable." },
    })
  })

	it("rejects malformed or unsupported status input", () => {
		expect(parseCliEnvelope("not json")).toBeUndefined()
		expect(parseCliEnvelope(JSON.stringify({ kind: "status", phase: "ready", started: true }))).toBeUndefined()
		expect(parseCliEnvelope(JSON.stringify({ kind: "status", schemaVersion: 1, phase: "ready", state: "failed", errorInfo: { message: "boom" } }))).toBeUndefined()
		expect(parseCliEnvelope(JSON.stringify({ kind: "status", schemaVersion: 1, phase: "ready", state: "failed", error: "boom" }))).toBeUndefined()
		expect(parseCliEnvelope(JSON.stringify({ kind: "status", schemaVersion: 1, phase: "ready", state: "failed", error: { code: "x" } }))).toBeUndefined()
		expect(parseCliEnvelope(JSON.stringify({ kind: "status", schemaVersion: 1, phase: "ready", state: "failed", error: { message: "x", details: "old" } }))).toBeUndefined()
		expect(parseCliEnvelope(JSON.stringify({ kind: "status", schemaVersion: 1, phase: "ready", state: "failed" }))).toBeUndefined()
		expect(parseCliEnvelope(JSON.stringify({ kind: "status", schemaVersion: 2, phase: "ready", state: "started" }))).toBeUndefined()
    expect(parseCliEnvelope(JSON.stringify({ kind: "status", schemaVersion: 1, phase: "ready", state: "started", operationId: 17 }))).toBeUndefined()
    expect(parseCliEnvelope(JSON.stringify({ kind: "status", schemaVersion: 1, phase: "ready", state: "started", durationMs: "17" }))).toBeUndefined()
    expect(parseCliEnvelope(JSON.stringify({ kind: "status", schemaVersion: 1, phase: "ready", state: "started", error: { message: "x", context: { attempt: 1 } } }))).toBeUndefined()
    expect(parseCliEnvelope(JSON.stringify({ kind: "diagnostic", message: "raw" }))).toBeUndefined()
  })

  it("keeps legacy zap errors when context is a flattened string", () => {
    const legacy = {
      level: "error",
      cliError: { code: "BUILD_FAILED", message: "build failed", context: "map[attempt:1]" },
    }
    expect(cliErrorFromLegacy(legacy)).toMatchObject({ code: "BUILD_FAILED", message: "build failed" })
    expect(cliErrorFromLegacy(legacy)?.context).toBeUndefined()
    const structured = {
      level: "error",
      cliError: { code: "BUILD_FAILED", message: "build failed", context: { attempt: "1" } },
    }
    expect(cliErrorFromLegacy(structured)?.context).toEqual({ attempt: "1" })
  })
})
