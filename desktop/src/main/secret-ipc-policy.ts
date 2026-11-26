import type { BrowserWindow, IpcMainInvokeEvent } from "electron"

function documentKey(value: unknown): string | undefined {
  if (typeof value !== "string" || value.length === 0) return undefined
  try {
    const url = new URL(value)
    if (url.username || url.password) return undefined
    return `${url.protocol}//${url.host}${url.pathname}`
  } catch {
    return undefined
  }
}

/** Restrict secret IPC to the live top-level document in the main app window. */
export function isTrustedSecretIpcSender(
  event: IpcMainInvokeEvent,
  win: BrowserWindow | null,
  expectedDocumentURL: string,
): boolean {
  if (!event || !win) return false

  try {
    if (win.isDestroyed()) return false
    const contents = win.webContents
    if (!contents || contents.isDestroyed() || event.sender !== contents)
      return false

    const frame = event.senderFrame
    if (!frame || frame !== contents.mainFrame) return false

    const expected = documentKey(expectedDocumentURL)
    const actual = documentKey(frame.url)
    return expected !== undefined && actual !== undefined && actual === expected
  } catch {
    // Electron may invalidate frame/window objects during navigation or teardown.
    return false
  }
}
