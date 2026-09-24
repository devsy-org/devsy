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
  let timer: ReturnType<typeof setTimeout> | undefined
  try {
    const operation = deps.getNameOwner
      ? deps.getNameOwner(WATCHER_NAME)
      : sessionNameOwner(WATCHER_NAME)
    const owner = await Promise.race([
      operation,
      new Promise<never>((_, reject) => {
        timer = setTimeout(
          () => reject(new Error("tray host probe timed out")),
          deps.timeoutMs ?? DEFAULT_TIMEOUT_MS,
        )
      }),
    ])
    return typeof owner === "string" && owner.length > 0
  } catch {
    return false
  } finally {
    if (timer) clearTimeout(timer)
  }
}

async function sessionNameOwner(name: string): Promise<string> {
  const { sessionBus } = await import("dbus-next")
  const bus = sessionBus()
  try {
    const dbus = await bus.getProxyObject(
      "org.freedesktop.DBus",
      "/org/freedesktop/DBus",
    )
    const iface = dbus.getInterface("org.freedesktop.DBus")
    return (await iface.GetNameOwner(name)) as string
  } finally {
    bus.disconnect()
  }
}
