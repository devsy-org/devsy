import { randomUUID } from "node:crypto"

export interface PortalBackgroundResponse {
  background: boolean
  autostart: boolean
}

export interface PortalBackgroundOptions {
  reason: string
  autostart: boolean
  commandline?: string[]
}

// The portal Response signal is delivered on a Request object whose path is
// only known after the method returns. Precomputing the path from the
// handle_token lets us subscribe before the call so an instant response
// cannot race past the listener.
export function portalRequestPath(senderUniqueName: string, token: string): string {
  const sender = senderUniqueName.replace(/^:/, "").replace(/\./g, "_")
  return `/org/freedesktop/portal/desktop/request/${sender}/${token}`
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
    const path = portalRequestPath(bus.name, token)
    const requestObject = await bus.getProxyObject(
      "org.freedesktop.portal.Desktop",
      path,
    )
    const request = requestObject.getInterface("org.freedesktop.portal.Request")
    const response = new Promise<PortalBackgroundResponse>((resolve) => {
      const timer = setTimeout(() => {
        resolve({ background: false, autostart: false })
      }, PORTAL_TIMEOUT_MS)
      request.on(
        "Response",
        (code: number, results: Record<string, { value: unknown }>) => {
          clearTimeout(timer)
          resolve(parsePortalResponse(code, results))
        },
      )
    })
    const methodOptions: Record<string, InstanceType<typeof Variant>> = {
      handle_token: new Variant("s", token),
      reason: new Variant("s", options.reason),
      autostart: new Variant("b", options.autostart),
    }
    if (options.commandline) {
      methodOptions.commandline = new Variant("as", options.commandline)
    }
    await background.RequestBackground("", methodOptions)
    return await response
  } finally {
    bus.disconnect()
  }
}
