import type { BrowserWindow, IpcMainEvent, IpcMainInvokeEvent } from "electron"
import {
  maxRendererErrorMessageLength,
  type RendererErrorSummary,
  rendererErrorMessages,
  rendererErrorNames,
  sanitizeRendererErrorMessage,
} from "../shared/renderer-error.js"
import { isTrustedSecretIpcSender } from "./secret-ipc-policy.js"

const MAX_PAYLOAD_BYTES = maxRendererErrorMessageLength * 4 + 128
const MAX_REPORTS = 10
const WINDOW_MS = 60_000

function parseSummary(
  payload: unknown,
  registered: readonly string[],
): RendererErrorSummary | undefined {
  try {
    if (!payload || typeof payload !== "object" || Array.isArray(payload))
      return undefined
    const keys = Object.keys(payload)
    if (
      keys.length !== 3 ||
      !keys.includes("kind") ||
      !keys.includes("message") ||
      !keys.includes("name")
    )
      return undefined
    const { kind, message, name } = payload as Record<string, unknown>
    if (
      typeof kind !== "string" ||
      kind.length > 32 ||
      typeof message !== "string" ||
      message.length === 0 ||
      message.length > maxRendererErrorMessageLength ||
      typeof name !== "string" ||
      name.length > 32
    )
      return undefined
    if (
      Buffer.byteLength(kind) +
        Buffer.byteLength(message) +
        Buffer.byteLength(name) >
      MAX_PAYLOAD_BYTES
    )
      return undefined
    if (kind !== "error" && kind !== "unhandledrejection") return undefined
    if (!rendererErrorNames.some((value) => value === name)) return undefined
    return {
      kind,
      message:
        sanitizeRendererErrorMessage(message, registered) ||
        rendererErrorMessages[kind],
      name: name as RendererErrorSummary["name"],
    }
  } catch {
    return undefined
  }
}

export function createRendererErrorHandler(options: {
  getWindow: () => BrowserWindow | null
  expectedDocumentURL: string
  log: (summary: RendererErrorSummary) => void
  registeredValues: () => readonly string[]
  now?: () => number
}): (event: IpcMainEvent, payload: unknown) => void {
  let windowStart: number | undefined
  let reports = 0
  const now = options.now ?? Date.now
  return (event, payload) => {
    if (
      !isTrustedSecretIpcSender(
        event as unknown as IpcMainInvokeEvent,
        options.getWindow(),
        options.expectedDocumentURL,
      )
    )
      return
    const summary = parseSummary(payload, options.registeredValues())
    if (!summary) return
    const time = now()
    if (
      windowStart === undefined ||
      time < windowStart ||
      time - windowStart >= WINDOW_MS
    ) {
      windowStart = time
      reports = 0
    }
    if (reports >= MAX_REPORTS) return
    reports++
    options.log(summary)
  }
}
