export interface CLIError {
  code: string
  message: string
  hint?: string
  context?: Record<string, string>
}

export interface CliLogLine {
  level: "trace" | "debug" | "info" | "warn" | "error" | "fatal"
  ts: string
  msg: string
  [key: string]: unknown
}

export function isCliDiagnostic(value: unknown): value is CliLogLine {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false
  const candidate = value as Record<string, unknown>
  return (
    typeof candidate.ts === "string" &&
    typeof candidate.msg === "string" &&
    typeof candidate.level === "string" &&
    ["trace", "debug", "info", "warn", "error", "fatal"].includes(
      candidate.level,
    )
  )
}

export type CliEnvelopeKind = "status" | "result" | "error"

export interface CliStatusEnvelope {
  kind: "status"
  schemaVersion: 1
  phase: string
  pipeline?: string
  step?: string
  operationId?: string
  parentOperationId?: string
  state: "started" | "succeeded" | "failed" | "skipped"
  durationMs?: number
  error?: {
    code?: string
    message: string
    hint?: string
    context?: Record<string, string>
  }
}

export interface OperationStatus {
  phase: string
  pipeline?: string
  step?: string
  state: "started" | "succeeded" | "failed" | "skipped"
  operationId?: string
  parentOperationId?: string
  durationMs?: number
  error?: CliStatusEnvelope["error"]
}

export interface CliResultEnvelope {
  kind: "result"
  outcome: "success"
  containerId: string
  remoteUser: string
  remoteWorkspaceFolder: string
  url?: string
  warnings?: string[]
  recovery?: boolean
  /** Durable history identity. Absent from envelopes written by older CLIs. */
  runId?: string
}

export interface CliErrorEnvelope {
  kind: "error"
  outcome: "error"
  message: string
  code?: string
  hint?: string
  context?: Record<string, string>
}

const RUN_UUID =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/

export interface RunIdentity {
  runId?: string
  failedInvocationId?: string
}

/**
 * Reads optional history identity from string-valued error context. Values
 * that are not canonical UUIDs are dropped, never trusted.
 */
export function runIdentityFromContext(
  context: Record<string, string> | undefined,
): RunIdentity {
  const identity: RunIdentity = {}
  if (context?.runId && RUN_UUID.test(context.runId))
    identity.runId = context.runId
  if (
    identity.runId &&
    context?.failedInvocationId &&
    RUN_UUID.test(context.failedInvocationId)
  )
    identity.failedInvocationId = context.failedInvocationId
  return identity
}

/** Run ID from a result envelope or task submission, when present and valid. */
export function runIdFromPayload(value: unknown): string | undefined {
  if (!value || typeof value !== "object") return undefined
  const runId = (value as { runId?: unknown }).runId
  return typeof runId === "string" && RUN_UUID.test(runId) ? runId : undefined
}

export function cliErrorFromEnvelope(value: unknown): CLIError | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value))
    return undefined
  const candidate = value as Record<string, unknown>
  if (
    Object.keys(candidate).some(
      (key) =>
        !["kind", "outcome", "code", "message", "hint", "context"].includes(
          key,
        ),
    )
  )
    return undefined
  if (candidate.kind !== "error" || candidate.outcome !== "error")
    return undefined
  if (typeof candidate.message !== "string" || candidate.message.length === 0)
    return undefined
  if (candidate.code !== undefined && typeof candidate.code !== "string")
    return undefined
  if (candidate.hint !== undefined && typeof candidate.hint !== "string")
    return undefined
  if (candidate.context !== undefined && !isStringMap(candidate.context))
    return undefined
  return {
    code: typeof candidate.code === "string" ? candidate.code : "UNKNOWN",
    message: candidate.message,
    hint: typeof candidate.hint === "string" ? candidate.hint : undefined,
    context: candidate.context as Record<string, string> | undefined,
  }
}

function isStringMap(value: unknown): value is Record<string, string> {
  return (
    !!value &&
    typeof value === "object" &&
    !Array.isArray(value) &&
    Object.values(value).every((entry) => typeof entry === "string")
  )
}

export type CliEnvelope =
  | CliStatusEnvelope
  | CliResultEnvelope
  | CliErrorEnvelope

/** Returns undefined when line isn't a recognized envelope. */
export function parseCliEnvelope(line: string): CliEnvelope | undefined {
  const trimmed = line.trim()
  if (!trimmed.startsWith("{")) return undefined
  try {
    const obj = JSON.parse(trimmed) as unknown
    if (isCliDiagnostic(obj)) return undefined
    if (
      obj &&
      typeof obj === "object" &&
      "kind" in obj &&
      (obj as { kind: unknown }).kind &&
      ["status", "result", "error"].includes((obj as { kind: string }).kind)
    ) {
      if ((obj as { kind: string }).kind === "status") {
        if (!isCurrentStatusEnvelope(obj)) {
          return undefined
        }
      }
      if (
        (obj as { kind: string }).kind === "error" &&
        !cliErrorFromEnvelope(obj)
      )
        return undefined
      if (obj.kind === "result" && !isResultEnvelope(obj)) return undefined
      return obj as CliEnvelope
    }
  } catch {
    // not JSON - fall through
  }
  return undefined
}

function isResultEnvelope(value: object): value is CliResultEnvelope {
  const candidate = value as Record<string, unknown>
  if (
    Object.keys(candidate).some(
      (key) =>
        ![
          "kind",
          "outcome",
          "containerId",
          "remoteUser",
          "remoteWorkspaceFolder",
          "url",
          "warnings",
          "recovery",
          "runId",
        ].includes(key),
    )
  )
    return false
  return (
    candidate.outcome === "success" &&
    typeof candidate.containerId === "string" &&
    typeof candidate.remoteUser === "string" &&
    typeof candidate.remoteWorkspaceFolder === "string" &&
    (candidate.url === undefined || typeof candidate.url === "string") &&
    (candidate.recovery === undefined ||
      typeof candidate.recovery === "boolean") &&
    (candidate.runId === undefined ||
      (typeof candidate.runId === "string" &&
        RUN_UUID.test(candidate.runId))) &&
    (candidate.warnings === undefined ||
      (Array.isArray(candidate.warnings) &&
        candidate.warnings.every((item) => typeof item === "string")))
  )
}

function isCurrentStatusEnvelope(value: object): value is CliStatusEnvelope {
  if (!hasOnlyCurrentStatusFields(value)) return false
  const candidate = value as Record<string, unknown>
  if (
    candidate.schemaVersion !== 1 ||
    typeof candidate.phase !== "string" ||
    candidate.phase.length === 0 ||
    !isStatusState(candidate.state)
  ) {
    return false
  }
  for (const field of [
    "pipeline",
    "operationId",
    "parentOperationId",
    "step",
  ]) {
    if (
      candidate[field] !== undefined &&
      typeof candidate[field] !== "string"
    ) {
      return false
    }
  }
  if (
    candidate.durationMs !== undefined &&
    (typeof candidate.durationMs !== "number" ||
      !Number.isFinite(candidate.durationMs) ||
      candidate.durationMs < 0)
  ) {
    return false
  }
  if (candidate.state === "failed" && candidate.error === undefined)
    return false
  return candidate.error === undefined || isStatusError(candidate.error)
}

function hasOnlyCurrentStatusFields(value: object): boolean {
  const fields = new Set([
    "kind",
    "schemaVersion",
    "pipeline",
    "operationId",
    "parentOperationId",
    "phase",
    "step",
    "state",
    "durationMs",
    "error",
  ])
  return Object.keys(value).every((field) => fields.has(field))
}

/** Converts a validated current status envelope to renderer state. */
export function normalizeOperationStatus(
  envelope: CliStatusEnvelope,
): OperationStatus {
  return {
    phase: envelope.phase,
    pipeline: envelope.pipeline,
    step: envelope.step,
    state: envelope.state,
    operationId: envelope.operationId,
    parentOperationId: envelope.parentOperationId,
    durationMs: envelope.durationMs,
    error: envelope.error,
  }
}

function isStatusState(value: unknown): value is CliStatusEnvelope["state"] {
  return (
    value === "started" ||
    value === "succeeded" ||
    value === "failed" ||
    value === "skipped"
  )
}

function isStatusError(
  value: unknown,
): value is NonNullable<CliStatusEnvelope["error"]> {
  if (!value || typeof value !== "object") return false
  const error = value as Record<string, unknown>
  const fields = new Set(["code", "message", "hint", "context"])
  if (Object.keys(error).some((field) => !fields.has(field))) return false
  if (
    typeof error.message !== "string" ||
    (error.code !== undefined && typeof error.code !== "string") ||
    (error.hint !== undefined && typeof error.hint !== "string")
  ) {
    return false
  }
  if (error.context !== undefined) {
    if (
      !error.context ||
      typeof error.context !== "object" ||
      Array.isArray(error.context)
    ) {
      return false
    }
    if (
      !Object.values(error.context).every((entry) => typeof entry === "string")
    ) {
      return false
    }
  }
  return true
}
