export interface CLIError {
  code: string
  message: string
  hint?: string
  context?: Record<string, string>
}

export interface CliLogLine {
  level?: "debug" | "info" | "warn" | "error" | "panic" | "fatal" | string
  ts?: string
  msg?: string
  cliError?: CLIError
  [key: string]: unknown
}

export type CliEnvelopeKind = "status" | "result" | "error"

export interface CliStatusEnvelope {
  kind: "status"
  schemaVersion: 1
  phase: string
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
}

export interface CliErrorEnvelope {
  kind: "error"
  outcome: "error"
  message: string
  code?: string
  hint?: string
  context?: Record<string, string>
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
      return obj as CliEnvelope
    }
  } catch {
    // not JSON — fall through
  }
  return undefined
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
  for (const field of ["pipeline", "operationId", "parentOperationId", "step"]) {
    if (candidate[field] !== undefined && typeof candidate[field] !== "string") {
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
  if (candidate.state === "failed" && candidate.error === undefined) return false
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
    step: envelope.step,
    state: envelope.state,
    operationId: envelope.operationId,
    parentOperationId: envelope.parentOperationId,
    durationMs: envelope.durationMs,
    error: envelope.error,
  }
}

function isStatusState(value: unknown): value is CliStatusEnvelope["state"] {
  return value === "started" || value === "succeeded" || value === "failed" || value === "skipped"
}

function isStatusError(value: unknown): value is NonNullable<CliStatusEnvelope["error"]> {
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
    if (!error.context || typeof error.context !== "object" || Array.isArray(error.context)) {
      return false
    }
    if (!Object.values(error.context).every((entry) => typeof entry === "string")) {
      return false
    }
  }
  return true
}
