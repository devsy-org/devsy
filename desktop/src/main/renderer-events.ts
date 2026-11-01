import type { BrowserWindow } from "electron"
import { mainLog } from "./logging.js"

export function sendRendererEvent(
  getWindow: () => BrowserWindow | null,
  channel: string,
  payload: unknown,
): void {
  try {
    const window = getWindow()
    if (!window || window.isDestroyed?.() || window.webContents.isDestroyed?.())
      return
    window.webContents.send(channel, payload)
  } catch {
    mainLog.warn(`[renderer] ${channel} delivery unavailable`)
  }
}
