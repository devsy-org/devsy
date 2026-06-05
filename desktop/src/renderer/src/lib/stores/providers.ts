import { writable } from "svelte/store"
import { providerList } from "$lib/ipc/commands.js"
import { onProvidersChanged } from "$lib/ipc/events.js"
import type { UnlistenFn } from "$lib/ipc/types.js"
import type { Provider } from "$lib/types/index.js"

export const providers = writable<Provider[]>([])
export const providersLoading = writable(true)

// Names of providers whose initialization is currently in flight. Lets cards
// show an "initializing…" state instead of the red "not initialized" badge
// during the multi-second init that runs after a provider is added.
export const initializingProviders = writable<Set<string>>(new Set())

export function markInitializing(name: string) {
  initializingProviders.update((set) => new Set(set).add(name))
}

export function clearInitializing(name: string) {
  initializingProviders.update((set) => {
    const next = new Set(set)
    next.delete(name)
    return next
  })
}

let unlisten: UnlistenFn | null = null

export async function initProviders() {
  providersLoading.set(true)
  try {
    const list = await providerList()
    providers.set(list)
  } catch {
    // IPC not available
  } finally {
    providersLoading.set(false)
  }

  try {
    unlisten = await onProvidersChanged((updated) => {
      providers.set(updated)
    })
  } catch {
    // Event listener setup failed
  }
}

export function destroyProviders() {
  if (unlisten) {
    unlisten()
    unlisten = null
  }
}
