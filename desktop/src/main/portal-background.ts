import { randomUUID } from "node:crypto"
import type { Message } from "dbus-next"

export interface PortalBackgroundResponse {
  background: boolean
  autostart: boolean
}

export interface PortalBackgroundOptions {
  reason: string
  autostart: boolean
  commandline?: string[]
}

export function parsePortalResponse(
  code: number,
  results: Record<string, { value: unknown }> | undefined,
): PortalBackgroundResponse {
  if (code !== 0) return { background: false, autostart: false }
  return {
    background: results?.background?.value === true,
    autostart: results?.autostart?.value === true,
  }
}

const PORTAL_TIMEOUT_MS = 120_000

export async function requestPortalBackground(
  options: PortalBackgroundOptions,
): Promise<PortalBackgroundResponse> {
  // Imported lazily so non-Flatpak platforms never load the D-Bus stack.
  const { sessionBus, Variant } = await import("dbus-next")
  const bus = sessionBus()
  try {
    const desktop = await bus.getProxyObject(
      "org.freedesktop.portal.Desktop",
      "/org/freedesktop/portal/desktop",
    )
    const background = desktop.getInterface("org.freedesktop.portal.Background")
    const token = `devsy${randomUUID().replace(/-/g, "")}`
    let requestPath: string | undefined
    const pendingResponses = new Map<string, PortalBackgroundResponse>()
    let resolveResponse: ((value: PortalBackgroundResponse) => void) | undefined
    const response = new Promise<PortalBackgroundResponse>((resolve) => {
      resolveResponse = resolve
    })
    const onMessage = (message: Message) => {
      if (
        message.type !== 4 ||
        message.interface !== "org.freedesktop.portal.Request" ||
        message.member !== "Response" ||
        !message.path || (requestPath !== undefined && message.path !== requestPath)
      ) return
      const [code, results] = message.body
      const parsed = parsePortalResponse(
        Number(code),
        results as Record<string, { value: unknown }> | undefined,
      )
      if (requestPath === undefined) pendingResponses.set(message.path, parsed)
      else resolveResponse?.(parsed)
    }
    bus.on("message", onMessage)
    const timer = setTimeout(() => {
      resolveResponse?.({ background: false, autostart: false })
    }, PORTAL_TIMEOUT_MS)
    const methodOptions: Record<string, InstanceType<typeof Variant>> = {
      handle_token: new Variant("s", token),
      reason: new Variant("s", options.reason),
      autostart: new Variant("b", options.autostart),
    }
    if (options.commandline) {
      methodOptions.commandline = new Variant("as", options.commandline)
    }
    try {
      requestPath = (await background.RequestBackground("", methodOptions)) as string
      const pending = pendingResponses.get(requestPath)
      if (pending) resolveResponse?.(pending)
      return await response
    } finally {
      clearTimeout(timer)
      bus.removeListener("message", onMessage)
    }
  } finally {
    bus.disconnect()
  }
}
