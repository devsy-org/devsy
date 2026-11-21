import {
  maxRendererErrorMessageLength,
  type RendererErrorKind,
  type RendererErrorName,
  type RendererErrorSummary,
  rendererErrorMessages,
  rendererErrorNames,
} from "$shared/renderer-error.js"

function errorName(value: unknown): RendererErrorName {
  try {
    if (value instanceof Error) {
      const name = value.name
      if (rendererErrorNames.some((allowed) => allowed === name))
        return name as RendererErrorName
    }
  } catch {
    return "UnknownError"
  }
  return "UnknownError"
}

export function installRendererErrorReporting(
  target: Window,
  report: (summary: RendererErrorSummary) => void,
): () => void {
  const send = (kind: RendererErrorKind, value: unknown) => {
    try {
      let message = ""
      if (typeof value === "string") message = value
      else if (value instanceof Error) {
        const candidate = value.message
        if (typeof candidate === "string") message = candidate
      }
      report({
        kind,
        message:
          message.length > 0 && message.length <= maxRendererErrorMessageLength
            ? message
            : rendererErrorMessages[kind],
        name: errorName(value),
      })
    } catch {}
  }
  const onError = (event: ErrorEvent) =>
    send("error", event.error ?? event.message)
  const onRejection = (event: PromiseRejectionEvent) =>
    send("unhandledrejection", event.reason)
  target.addEventListener("error", onError)
  target.addEventListener("unhandledrejection", onRejection)
  return () => {
    target.removeEventListener("error", onError)
    target.removeEventListener("unhandledrejection", onRejection)
  }
}
