// biome-ignore lint/suspicious/noControlCharactersInRegex: ANSI escape stripping requires matching ESC (0x1b)
const ANSI_RE = /\x1b\[[0-9;]*m/g
const BRACKET_RE = /\[0[;0-9]*m/g

/** Strip ANSI escape codes from a string */
export function stripAnsi(str: string): string {
  return str.replace(ANSI_RE, "").replace(BRACKET_RE, "")
}

export function isCommandSuccess(message: string | undefined | null): boolean {
  if (!message) return false
  const clean = stripAnsi(message)
  return clean.includes("Exit code: 0") || clean.includes('"outcome":"success"')
}

export interface ParsedLogLine {
  time: string
  level: "info" | "warn" | "fatal" | "debug" | "error" | ""
  message: string
  source: string
  origin: "cli" | "tunnel" | ""
}

/** Try to extract an inner structured log embedded in the message field. */
function tryParseInnerLog(
  message: string,
): { level: ParsedLogLine["level"]; msg: string } | null {
  // Handle raw JSON messages like {"level":"debug","msg":"..."}
  if (message.startsWith("{")) {
    try {
      const parsed = JSON.parse(message)
      if (parsed.msg) {
        const level = (parsed.level?.toLowerCase() ??
          "") as ParsedLogLine["level"]
        return {
          level: ["info", "warn", "fatal", "debug", "error"].includes(level)
            ? level
            : "info",
          msg: parsed.msg,
        }
      }
    } catch {
      /* not valid JSON */
    }
  }

  // Pattern: ISO8601 timestamp, then DEBUG/INFO/WARN/ERROR, then source.go:NNN, then optional JSON
  const match = message.match(
    /^\d{4}-\d{2}-\d{2}T[^\s]+\s+(DEBUG|INFO|WARN|ERROR)\s+\S+\.\w+:\d+\s*(.*)$/,
  )
  if (!match) return null

  const level = match[1].toLowerCase() as ParsedLogLine["level"]
  const rest = match[2].trim()

  // Try to parse JSON payload
  if (rest.startsWith("{")) {
    try {
      const parsed = JSON.parse(rest)
      if (parsed.msg) {
        return { level, msg: parsed.msg }
      }
    } catch {
      // Not valid JSON, use rest as message
    }
  }

  // Non-JSON inner message
  return { level, msg: rest || message }
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
    const outerLevel = zapMatch[2].toLowerCase() as ParsedLogLine["level"]
    const outerMessage = zapMatch[3]
    const outerSource = zapMatch[4] ?? ""

    // Check for embedded tunnel log
    const inner = tryParseInnerLog(outerMessage)
    if (inner) {
      return {
        time: timeMatch ? timeMatch[1] : zapMatch[1],
        level: inner.level,
        message: inner.msg,
        source: outerSource,
        origin: "tunnel",
      }
    }

    return {
      time: timeMatch ? timeMatch[1] : zapMatch[1],
      level: outerLevel,
      message: outerMessage,
      source: outerSource,
      origin: "cli",
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
      origin: "cli",
    }
  }

  // Continuation or unstructured line
  return { time: "", level: "", message: clean.trim(), source: "", origin: "" }
}
