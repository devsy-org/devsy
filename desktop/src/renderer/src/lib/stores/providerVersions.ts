import { writable } from "svelte/store"
import type {
  ProviderVersion,
  ProviderVersionCheckResult,
} from "$lib/types/index.js"
import {
  providerListVersions,
  providerCheckUpdates,
} from "$lib/ipc/commands.js"

type State = {
  byProvider: Record<
    string,
    { versions: ProviderVersion[]; unsupported: boolean; error?: string }
  >
  updates: Record<string, ProviderVersionCheckResult>
  lastCheckedAt: Date | null
}

const initial: State = {
  byProvider: {},
  updates: {},
  lastCheckedAt: null,
}

const internal = writable<State>(initial)

export const providerVersions = { subscribe: internal.subscribe }

export async function refreshUpdates(): Promise<void> {
  const updates = await providerCheckUpdates()
  internal.update((s) => ({ ...s, updates, lastCheckedAt: new Date() }))
}

export async function loadVersionsFor(name: string): Promise<void> {
  const result = await providerListVersions(name)
  internal.update((s) => ({
    ...s,
    byProvider: { ...s.byProvider, [name]: result },
  }))
}

export function resetProviderVersionsStore(): void {
  internal.set(initial)
}
