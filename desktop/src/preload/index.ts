import type { IpcRendererEvent } from "electron"
import { contextBridge, ipcRenderer } from "electron"
import type { RendererErrorSummary } from "../shared/renderer-error.js"

contextBridge.exposeInMainWorld("electronAPI", {
  reportRendererError: (summary: RendererErrorSummary): void => {
    ipcRenderer.send("renderer-error", summary)
  },

  invoke: (channel: string, args?: Record<string, unknown>): Promise<unknown> =>
    ipcRenderer.invoke(channel, args),

  on: (channel: string, callback: (payload: unknown) => void): (() => void) => {
    const listener = (_event: IpcRendererEvent, payload: unknown) =>
      callback(payload)
    ipcRenderer.on(channel, listener)
    return () => ipcRenderer.removeListener(channel, listener)
  },
})
