import { providerIcon } from "$lib/ipc/commands.js"
import { activeContext } from "$lib/stores/contexts.js"
import type { ProviderIconTheme } from "$shared/provider-icon.js"

const MAX_ENTRIES = 64

export interface ProviderIconLookup {
  providerName: string
  context: string
  theme: ProviderIconTheme
  revision: string
}

interface LookupEntry {
  context: string
  promise: Promise<string | null>
}

// Only definitive answers are kept: transport errors wait for the next lookup;
// stale replies get one fresh read while their context remains active.
const pendingLookups = new Map<string, LookupEntry>()
const settledLookups = new Map<string, Promise<string | null>>()
let currentContext: string | undefined

// Main revokes pending reads from the context that is no longer active.
activeContext.subscribe((context) => {
  if (currentContext !== undefined && currentContext !== context) {
    for (const [key, entry] of pendingLookups) {
      if (entry.context === currentContext) {
        pendingLookups.delete(key)
      }
    }
  }
  currentContext = context
})

function lookupKey({
  context,
  providerName,
  revision,
  theme,
}: ProviderIconLookup): string {
  return `${context}\0${providerName}\0${revision}\0${theme}`
}

/** Resolves to a `data:` URL, or null when the image is missing or unavailable. */
export function loadProviderIcon(
  lookup: ProviderIconLookup,
): Promise<string | null> {
  const key = lookupKey(lookup)
  const cached = settledLookups.get(key)
  if (cached) return cached
  const existing = pendingLookups.get(key)
  if (existing) return existing.promise

  let entry: LookupEntry | undefined
  const pending = providerIcon({
    providerName: lookup.providerName,
    context: lookup.context,
    theme: lookup.theme,
    expectedRevision: lookup.revision,
  }).then(
    (reply) => {
      if ("stale" in reply) {
        if (!isCurrentLookup(key, lookup, entry)) {
          if (entry && pendingLookups.get(key) === entry) {
            pendingLookups.delete(key)
          }
          return null
        }
        return retryStaleProviderIcon(lookup, key, entry)
      }
      const result = "dataUrl" in reply ? reply.dataUrl : null
      cacheSettledProviderIcon(key, lookup, entry, result)
      return result
    },
    () => {
      if (entry && pendingLookups.get(key) === entry) {
        pendingLookups.delete(key)
      }
      return null
    },
  )
  entry = { context: lookup.context, promise: pending }
  pendingLookups.set(key, entry)
  return pending
}

function isCurrentLookup(
  key: string,
  lookup: ProviderIconLookup,
  entry: LookupEntry | undefined,
): entry is LookupEntry {
  return (
    entry !== undefined &&
    pendingLookups.get(key) === entry &&
    currentContext === lookup.context
  )
}

function cacheSettledProviderIcon(
  key: string,
  lookup: ProviderIconLookup,
  entry: LookupEntry | undefined,
  result: string | null,
): void {
  if (!isCurrentLookup(key, lookup, entry)) return
  pendingLookups.delete(key)
  settledLookups.set(key, Promise.resolve(result))
  while (settledLookups.size > MAX_ENTRIES) {
    const oldest = settledLookups.keys().next().value
    if (oldest === undefined) break
    settledLookups.delete(oldest)
  }
}

function retryStaleProviderIcon(
  lookup: ProviderIconLookup,
  key: string,
  entry: LookupEntry,
): Promise<string | null> {
  return providerIcon({
    providerName: lookup.providerName,
    context: lookup.context,
    theme: lookup.theme,
    expectedRevision: lookup.revision,
  }).then(
    (reply) => {
      if ("stale" in reply) {
        if (pendingLookups.get(key) === entry) pendingLookups.delete(key)
        return null
      }
      const result = "dataUrl" in reply ? reply.dataUrl : null
      cacheSettledProviderIcon(key, lookup, entry, result)
      return result
    },
    () => {
      if (pendingLookups.get(key) === entry) pendingLookups.delete(key)
      return null
    },
  )
}

export function clearProviderIconLoader(): void {
  pendingLookups.clear()
  settledLookups.clear()
}
