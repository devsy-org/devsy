/**
 * Extract a human-readable message from CLI error output.
 * The CLI errors contain a FATAL line with the actual message after the file reference.
 * Pattern: '... FATAL <file>:<line> <actual message>'
 * Falls back to the raw string if no FATAL line is found.
 */
export function extractErrorMessage(err: unknown): string {
  const raw = err instanceof Error ? err.message : String(err)
  // Match: FATAL <filepath>:<number> <message>
  const fatalMatch = raw.match(/FATAL\s+\S+:\d+\s+(.+)/i)
  if (fatalMatch) return fatalMatch[1].trim()
  // Fallback: try to get the last meaningful segment after 'Error:'
  const parts = raw.split("Error:")
  const last = parts[parts.length - 1]?.trim()
  return last || raw
}
