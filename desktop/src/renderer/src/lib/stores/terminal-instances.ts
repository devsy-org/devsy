import type { FitAddon } from "@xterm/addon-fit"
import type { Terminal } from "@xterm/xterm"

export interface TerminalInstance {
  term: Terminal
  fitAddon: FitAddon
  unlistenOutput?: () => void
  unlistenExit?: () => void
  unsubscribeTheme?: () => void
}

const instances = new Map<string, TerminalInstance>()

export function getTerminalInstance(
  sessionId: string,
): TerminalInstance | undefined {
  return instances.get(sessionId)
}

export function setTerminalInstance(
  sessionId: string,
  instance: TerminalInstance,
): void {
  instances.set(sessionId, instance)
}

export function destroyTerminalInstance(sessionId: string): void {
  const instance = instances.get(sessionId)
  if (instance) {
    instance.unlistenOutput?.()
    instance.unlistenExit?.()
    instance.unsubscribeTheme?.()
    instance.term.dispose()
    instances.delete(sessionId)
  }
}

export function hasTerminalInstance(sessionId: string): boolean {
  return instances.has(sessionId)
}
