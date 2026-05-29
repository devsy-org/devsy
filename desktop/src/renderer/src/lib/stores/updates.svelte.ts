import { onUpdateStatus, type UpdateStatus } from "$lib/ipc/events.js"
import type { UnlistenFn } from "$lib/ipc/types.js"

type Listener = (s: UpdateStatus) => void

const state: { current: UpdateStatus; lastCheckedAt: number | null } = $state({
  current: { state: "idle" },
  lastCheckedAt: null,
})

const listeners: Listener[] = []
let unlisten: UnlistenFn | null = null

export function updateStatus(): UpdateStatus {
  return state.current
}

export function lastCheckedAt(): number | null {
  return state.lastCheckedAt
}

export function hasUpdate(): boolean {
  const s = state.current.state
  return s === "available" || s === "downloading" || s === "downloaded"
}

export function isReady(): boolean {
  return state.current.state === "downloaded"
}

export function isChecking(): boolean {
  return state.current.state === "checking"
}

export function isDevMode(): boolean {
  return state.current.state === "not-available" && state.current.code === "dev-mode"
}

export function subscribe(fn: Listener): () => void {
  listeners.push(fn)
  return () => {
    const i = listeners.indexOf(fn)
    if (i >= 0) listeners.splice(i, 1)
  }
}

function set(next: UpdateStatus): void {
  state.current = next
  if (next.state === "not-available" || next.state === "available") {
    state.lastCheckedAt = Date.now()
  }
  for (const fn of listeners) fn(next)
}

export async function initUpdateStore(): Promise<void> {
  if (unlisten) return
  unlisten = await onUpdateStatus(set)
}

export function disposeUpdateStore(): void {
  unlisten?.()
  unlisten = null
}

// Test-only setter
export const __setForTest = set
