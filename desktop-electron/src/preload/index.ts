import { contextBridge, ipcRenderer } from "electron"
import type { IpcRendererEvent } from "electron"

contextBridge.exposeInMainWorld("electronAPI", {
  invoke: (channel: string, args?: Record<string, unknown>): Promise<unknown> =>
    ipcRenderer.invoke(channel, args),

  on: (channel: string, callback: (payload: unknown) => void): (() => void) => {
    const listener = (_event: IpcRendererEvent, payload: unknown) =>
      callback(payload)
    ipcRenderer.on(channel, listener)
    return () => ipcRenderer.removeListener(channel, listener)
  },
})
