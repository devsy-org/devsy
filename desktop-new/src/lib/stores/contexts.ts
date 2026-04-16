import type { UnlistenFn } from "@tauri-apps/api/event"
import { writable } from "svelte/store"
import { contextList } from "$lib/ipc/commands.js"
import { onContextsChanged } from "$lib/ipc/events.js"
import type { Context } from "$lib/types/index.js"

export const contexts = writable<Context[]>([])
export const activeContext = writable<string>("")

let unlisten: UnlistenFn | null = null

export async function initContexts() {
  try {
    const result = await contextList()
    contexts.set(result.contexts)
    activeContext.set(result.activeContext)
  } catch {
    // Tauri not available
  }

  try {
    unlisten = await onContextsChanged((updated, active) => {
      contexts.set(updated)
      activeContext.set(active)
    })
  } catch {
    // Event listener setup failed
  }
}

export function destroyContexts() {
  if (unlisten) {
    unlisten()
    unlisten = null
  }
}
