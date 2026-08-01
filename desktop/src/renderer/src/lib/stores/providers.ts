import { writable } from "svelte/store"
import { providerList } from "$lib/ipc/commands.js"
import { onProvidersChanged } from "$lib/ipc/events.js"
import type { UnlistenFn } from "$lib/ipc/types.js"
import type { Provider, ProviderJob } from "$lib/types/index.js"

export const providers = writable<Provider[]>([])
export const providersLoading = writable(true)

// In-flight provider work, keyed by provider name. Owned by the main process
// so it survives the wizard closing, navigation, and window reload.
export const providerJobs = writable<Record<string, ProviderJob>>({})

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
    unlisten = await onProvidersChanged((updated, jobs) => {
      providers.set(updated)
      providerJobs.set(jobs)
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
