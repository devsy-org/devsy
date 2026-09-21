// Linux trays need a StatusNotifierWatcher on the session bus. GNOME without
// an AppIndicator/KSNI extension has none, so auto-launching to the tray
// there would hide the app's only window.
export interface TrayHostProbeDeps {
  platform?: string
  getNameOwner?: (name: string) => Promise<string>
  timeoutMs?: number
}

const WATCHER_NAME = "org.kde.StatusNotifierWatcher"
const DEFAULT_TIMEOUT_MS = 1500

export async function isTrayHostAvailable(
  deps: TrayHostProbeDeps = {},
): Promise<boolean> {
  const platform = deps.platform ?? process.platform
  if (platform !== "linux") return true
  try {
    const getNameOwner = deps.getNameOwner ?? (await sessionNameOwner())
    const owner = await Promise.race([
      getNameOwner(WATCHER_NAME),
      new Promise<never>((_, reject) =>
        setTimeout(
          () => reject(new Error("tray host probe timed out")),
          deps.timeoutMs ?? DEFAULT_TIMEOUT_MS,
        ),
      ),
    ])
    return typeof owner === "string" && owner.length > 0
  } catch {
    return false
  }
}

async function sessionNameOwner(): Promise<(name: string) => Promise<string>> {
  const { sessionBus } = await import("dbus-next")
  const bus = sessionBus()
  const dbus = await bus.getProxyObject(
    "org.freedesktop.DBus",
    "/org/freedesktop/DBus",
  )
  const iface = dbus.getInterface("org.freedesktop.DBus")
  return async (name) => {
    try {
      return (await iface.GetNameOwner(name)) as string
    } finally {
      bus.disconnect()
    }
  }
}
