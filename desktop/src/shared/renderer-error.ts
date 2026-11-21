import { credentialAssignments } from "../../../logging/desktop/diagnostic-redaction.js"

export const rendererErrorMessages = {
  error: "Uncaught renderer error",
  unhandledrejection: "Unhandled renderer promise rejection",
} as const

export const rendererErrorNames = [
  "Error",
  "TypeError",
  "RangeError",
  "ReferenceError",
  "SyntaxError",
  "URIError",
  "EvalError",
  "AggregateError",
  "UnknownError",
] as const

export type RendererErrorName = (typeof rendererErrorNames)[number]
export type RendererErrorKind = keyof typeof rendererErrorMessages

export interface RendererErrorSummary {
  kind: RendererErrorKind
  message: string
  name: RendererErrorName
}

export const maxRendererErrorMessageLength = 4096

function registeredRanges(
  value: string,
  registered: readonly string[],
): [number, number][] {
  const ranges: [number, number][] = []
  for (const secret of registered) {
    if (!secret) continue
    for (
      let start = value.indexOf(secret);
      start >= 0;
      start = value.indexOf(secret, start + 1)
    )
      ranges.push([start, start + secret.length])
  }
  return ranges
}

function redactSecrets(value: string, registered: readonly string[]): string {
  const bounded = value.slice(0, maxRendererErrorMessageLength)
  const firstRecord = bounded.split(/[\r\n\u2028\u2029]/, 1)[0]
  const cutByLimit =
    firstRecord.length === bounded.length && value.length > bounded.length
  const ranges = registeredRanges(value, registered)
  for (const { keyStart, end } of credentialAssignments(firstRecord, true))
    ranges.push([
      keyStart,
      cutByLimit && end >= firstRecord.length ? value.length : end,
    ])
  ranges.sort(([left], [right]) => left - right)
  let result = ""
  let copied = 0
  let masked = -1
  for (const [start, end] of ranges) {
    if (start > masked) {
      result += `${value.slice(copied, start)}[redacted]`
      copied = start
    }
    if (end > masked) {
      masked = end
      copied = end
    }
  }
  return result + value.slice(copied)
}

export function sanitizeRendererErrorMessage(
  value: string,
  registered: readonly string[] = [],
): string {
  return redactSecrets(value, registered)
    .slice(0, maxRendererErrorMessageLength)
    .split(/[\r\n\u2028\u2029]/, 1)[0]
    .replace(/\b(?:bearer|basic)\s+[a-z0-9._~+/=-]+/gi, "[redacted]")
    .replace(/\beyJ[a-z0-9_-]+\.[a-z0-9_-]+\.[a-z0-9_-]+\b/gi, "[redacted]")
    .replace(/\b[a-z][a-z0-9+.-]*:\/\/[^\s"'<>]+/gi, "[url]")
    .replace(/(?:[a-z]:\\|\\\\|\/|~\/)(?:[^\s"'<>()[\]{};,]+)/gi, "[path]")
    .replace(/\?[^\s"'<>]*/g, "[query]")
    .replace(
      /\{[^}]*\}|\[(?!redacted\]|url\]|path\]|query\]|details\])[^\]]*\]/g,
      "[details]",
    )
    .replace(/\p{Cc}/gu, (character) => {
      const code = character.charCodeAt(0)
      return code <= 31 || code === 127 ? " " : character
    })
    .trim()
    .slice(0, 128)
}
