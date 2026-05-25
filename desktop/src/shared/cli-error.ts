/**
 * Structured CLI error contract.
 *
 * The Go CLI emits a final zap JSON log line on stderr containing a `cliError`
 * field shaped exactly like {@link CLIError}. The field name and its children
 * are an immutable wire contract — see
 * `docs/superpowers/specs/2026-05-25-structured-cli-errors-design.md`.
 *
 * This type is the single source of truth shared between the Electron main
 * process (which parses CLI stderr) and the renderer (which displays errors).
 */
export interface CLIError {
  /** Stable machine-readable code, e.g. "AWS_PROFILE_MISSING", "UNKNOWN". */
  code: string
  /** One-line user-facing summary. */
  message: string
  /** Actionable next step for the user. */
  hint?: string
  /** Optional link to documentation about this error. */
  docUrl?: string
  /** Optional provider name ("aws", "docker", etc.) the error originated from. */
  provider?: string
  /** Original error text from the underlying failure; useful for debugging. */
  cause?: string
}

/**
 * Shape of a zap JSON log line as emitted on stderr by the CLI when
 * `--log-output json` is set. The `cliError` field is present only on the
 * final error event.
 */
export interface CliLogLine {
  level?: "debug" | "info" | "warn" | "error" | "panic" | "fatal" | string
  ts?: string
  msg?: string
  cliError?: CLIError
  [key: string]: unknown
}
