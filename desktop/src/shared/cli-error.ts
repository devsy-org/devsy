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

export function cliErrorFromEnvelope(value: unknown): CLIError | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined
  const candidate = value as Record<string, unknown>
  if (candidate.kind !== "error" || candidate.outcome !== "error") return undefined
  if (typeof candidate.message !== "string" || candidate.message.length === 0) return undefined
  if (candidate.code !== undefined && typeof candidate.code !== "string") return undefined
  if (candidate.hint !== undefined && typeof candidate.hint !== "string") return undefined
  if (candidate.context !== undefined && !isStringMap(candidate.context)) return undefined
  return {
    code: typeof candidate.code === "string" ? candidate.code : "UNKNOWN",
    message: candidate.message,
    hint: typeof candidate.hint === "string" ? candidate.hint : undefined,
    context: candidate.context as Record<string, string> | undefined,
  }
}

export function cliErrorFromLegacy(value: unknown): CLIError | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined
  const candidate = value as Record<string, unknown>
  if (candidate.level !== "error" && candidate.level !== "fatal" && candidate.level !== "panic") return undefined
  return isCLIError(candidate.cliError) ? candidate.cliError : undefined
}

function isCLIError(value: unknown): value is CLIError {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false
  const candidate = value as Record<string, unknown>
  return typeof candidate.code === "string" && typeof candidate.message === "string" &&
    (candidate.hint === undefined || typeof candidate.hint === "string") &&
    (candidate.context === undefined || isStringMap(candidate.context))
}

function isStringMap(value: unknown): value is Record<string, string> {
  return !!value && typeof value === "object" && !Array.isArray(value) &&
    Object.values(value).every((entry) => typeof entry === "string")
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
      if ((obj as { kind: string }).kind === "error" && !cliErrorFromEnvelope(obj)) return undefined
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
