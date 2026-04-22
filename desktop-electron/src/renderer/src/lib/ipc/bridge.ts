/**
 * IPC bridge — uses Electron's contextBridge API when running in Electron,
 * falls back to mock implementations for browser-only development.
 */

import type { UnlistenFn } from "./types.js"

function isElectron(): boolean {
  return typeof window !== "undefined" && "electronAPI" in window
}

interface ElectronAPI {
  invoke: (channel: string, args?: Record<string, unknown>) => Promise<unknown>
  on: (channel: string, callback: (payload: unknown) => void) => () => void
}

declare global {
  interface Window {
    electronAPI?: ElectronAPI
  }
}

type InvokeFn = <T>(cmd: string, args?: Record<string, unknown>) => Promise<T>
type ListenFn = <T>(
  event: string,
  callback: (event: { payload: T }) => void,
) => Promise<UnlistenFn>

let _invoke: InvokeFn
let _listen: ListenFn

if (isElectron()) {
  const api = window.electronAPI!

  _invoke = <T>(cmd: string, args?: Record<string, unknown>): Promise<T> =>
    api.invoke(cmd, args) as Promise<T>

  _listen = <T>(
    event: string,
    callback: (event: { payload: T }) => void,
  ): Promise<UnlistenFn> => {
    const unlisten = api.on(event, (payload) => {
      callback({ payload: payload as T })
    })
    return Promise.resolve(unlisten)
  }
} else {
  console.info(
    "%c[DevPod] Running in browser mock mode",
    "color: #f59e0b; font-weight: bold",
  )
  const mock = await import("./mock.js")
  _invoke = mock.invoke as InvokeFn
  _listen = mock.listen as ListenFn
}

export const invoke = _invoke
export const listen = _listen
