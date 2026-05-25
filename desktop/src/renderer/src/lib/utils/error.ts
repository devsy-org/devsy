import type { CLIError } from "$shared/cli-error.js"

/**
 * Extract a structured CLIError from a thrown IPC error, if the main process
 * attached one in cli.ts/wrapError. Electron's structured-clone-based IPC
 * preserves the own-property across the renderer boundary.
 */
export function extractCliError(err: unknown): CLIError | null {
  if (err && typeof err === "object" && "cliError" in err) {
    const candidate = (err as { cliError?: unknown }).cliError
    if (
      candidate &&
      typeof candidate === "object" &&
      "code" in candidate &&
      "message" in candidate
    ) {
      return candidate as CLIError
    }
  }
  return null
}

/**
 * Human-readable message for toast/inline string display. Prefers the
 * structured `cliError.message`; falls back to the raw Error.message for
 * non-CLI errors (IPC bridge failures, renderer-thrown errors, etc.).
 */
export function extractErrorMessage(err: unknown): string {
  const cliError = extractCliError(err)
  if (cliError) return cliError.message
  return err instanceof Error ? err.message : String(err)
}
