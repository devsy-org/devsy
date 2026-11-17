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

export function sanitizeRendererErrorMessage(value: string): string {
  return value
    .slice(0, 4096)
    .split(/[\r\n\u2028\u2029]/, 1)[0]
    .replace(
      /\b(?:authorization|password|passwd|token|secret|api[_-]?key|credential|private[_-]?key|access[_-]?key)\b\s*["']?\s*(?::|=|\bis\b)\s*(?:(?:bearer|basic)\s+)?(?:"[^"]*"|'[^']*'|[^\s,;]+)/gi,
      "[redacted]",
    )
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
