import { invoke, listen } from "./bridge.js"
import type { UnlistenFn } from "./types.js"

export async function terminalCreate(
  cols: number,
  rows: number,
): Promise<string> {
  return invoke<string>("terminal_create", { cols, rows })
}

export async function terminalCreateSsh(
  workspaceId: string,
  cols: number,
  rows: number,
): Promise<string> {
  return invoke<string>("terminal_create_ssh", { workspaceId, cols, rows })
}

export async function terminalWrite(
  sessionId: string,
  data: number[],
): Promise<void> {
  return invoke("terminal_write", { sessionId, data })
}

export async function terminalResize(
  sessionId: string,
  cols: number,
  rows: number,
): Promise<void> {
  return invoke("terminal_resize", { sessionId, cols, rows })
}

export async function terminalClose(sessionId: string): Promise<void> {
  return invoke("terminal_close", { sessionId })
}

export async function terminalListSessions(): Promise<string[]> {
  return invoke<string[]>("terminal_list")
}

interface TerminalOutputPayload {
  sessionId: string
  data: number[]
}

interface TerminalExitPayload {
  sessionId: string
  exitCode?: number
  signal?: number
}

export function onTerminalOutput(
  callback: (sessionId: string, data: Uint8Array) => void,
): Promise<UnlistenFn> {
  return listen<TerminalOutputPayload>("terminal:output", (event) => {
    callback(event.payload.sessionId, new Uint8Array(event.payload.data))
  })
}

export function onTerminalExit(
  callback: (sessionId: string, exitCode?: number, signal?: number) => void,
): Promise<UnlistenFn> {
  return listen<TerminalExitPayload>("terminal:exit", (event) => {
    callback(
      event.payload.sessionId,
      event.payload.exitCode,
      event.payload.signal,
    )
  })
}
