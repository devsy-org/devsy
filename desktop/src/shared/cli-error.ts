export interface CLIError {
  code: string
  message: string
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
  phase: string
  step?: string
  started: boolean
  error?: string
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
      return obj as CliEnvelope
    }
  } catch {
    // not JSON — fall through
  }
  return undefined
}
