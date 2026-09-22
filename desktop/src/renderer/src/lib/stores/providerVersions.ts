import { writable } from "svelte/store"
import {
  providerCheckUpdates,
  providerGetUpdateCache,
  providerListVersions,
} from "$lib/ipc/commands.js"
import type {
  ProviderVersion,
  ProviderVersionCheckResult,
} from "$lib/types/index.js"

type State = {
  byProvider: Record<
    string,
    { versions: ProviderVersion[]; unsupported: boolean; error?: string }
  >
  updates: Record<string, ProviderVersionCheckResult>
  lastCheckedAt: Date | null
  refreshing: boolean
  refreshError: string | null
}

const initial: State = {
  byProvider: {},
  updates: {},
  lastCheckedAt: null,
  refreshing: false,
  refreshError: null,
}

const internal = writable<State>(initial)

export const providerVersions = { subscribe: internal.subscribe }

export async function loadCachedUpdates(): Promise<void> {
  const cached = await providerGetUpdateCache()
  internal.update((s) => ({
    ...s,
    updates: cached.updates,
    lastCheckedAt: cached.lastCheckedAt ? new Date(cached.lastCheckedAt) : null,
  }))
}

export async function refreshUpdates(): Promise<void> {
  internal.update((s) => ({ ...s, refreshing: true, refreshError: null }))
  try {
    const updates = await providerCheckUpdates()
    const failed = Object.values(updates).filter((result) => result.error).length
    internal.update((s) => {
      const merged = { ...s.updates }
      for (const [name, result] of Object.entries(updates)) {
        merged[name] = result.error && s.updates[name]
          ? { ...s.updates[name], error: result.error }
          : result
      }
      return {
        ...s,
        updates: merged,
        lastCheckedAt: new Date(),
        refreshing: false,
        refreshError:
          failed === 0
            ? null
            : failed === 1
              ? "One provider update check failed."
              : `${failed} provider update checks failed.`,
      }
    })
  } catch (error) {
    internal.update((s) => ({
      ...s,
      refreshing: false,
      refreshError: error instanceof Error ? error.message : String(error),
    }))
    throw error
  }
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
