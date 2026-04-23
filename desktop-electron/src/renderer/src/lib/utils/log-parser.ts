// biome-ignore lint/suspicious/noControlCharactersInRegex: ANSI escape stripping requires matching ESC (0x1b)
const ANSI_RE = /\x1b\[[0-9;]*m/g
const BRACKET_RE = /\[0[;0-9]*m/g

/** Strip ANSI escape codes from a string */
export function stripAnsi(str: string): string {
  return str.replace(ANSI_RE, "").replace(BRACKET_RE, "")
}

export interface ParsedLogLine {
  time: string
  level: "info" | "warn" | "fatal" | "debug" | "error" | ""
  message: string
  source: string
}

/**
 * Parse a Devsy CLI log line into structured fields.
 *
 * Supports two formats:
 * - Zap console (current): `2026-04-23T05:14:03.279-0500\tINFO\tmessage\tsource.go:NNN`
 * - Legacy: `HH:MM:SS level message source.go:NNN`
 *
 * ANSI codes are stripped before parsing.
 */
export function parseLogLine(raw: string): ParsedLogLine {
  const clean = stripAnsi(raw)

  // Zap console format (tab-separated): ISO8601\tLEVEL\tmessage\tsource.go:NNN
  const zapMatch = clean.match(
    /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}[^\t]*)\t(INFO|WARN|FATAL|DEBUG|ERROR)\t(.*?)(?:\t(\S+\.\w+:\d+))?\s*$/,
  )
  if (zapMatch) {
    // Extract just HH:MM:SS from the ISO8601 timestamp for display
    const timeMatch = zapMatch[1].match(/T(\d{2}:\d{2}:\d{2})/)
    return {
      time: timeMatch ? timeMatch[1] : zapMatch[1],
      level: zapMatch[2].toLowerCase() as ParsedLogLine["level"],
      message: zapMatch[3],
      source: zapMatch[4] ?? "",
    }
  }

  // Legacy format (space-separated): HH:MM:SS level message source.go:NNN
  const legacyMatch = clean.match(
    /^(\d{1,2}:\d{2}:\d{2})\s+(info|warn|fatal|debug|error)\s+(.*?)\s+(\S+\.\w+:\d+)\s*$/,
  )
  if (legacyMatch) {
    return {
      time: legacyMatch[1],
      level: legacyMatch[2] as ParsedLogLine["level"],
      message: legacyMatch[3],
      source: legacyMatch[4],
    }
  }

  // Continuation or unstructured line
  return { time: "", level: "", message: clean.trim(), source: "" }
}
