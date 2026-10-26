import { providerIcon } from "$lib/ipc/commands.js"
import type { ProviderIconTheme } from "$shared/provider-icon.js"

const MAX_ENTRIES = 64

export interface ProviderIconLookup {
  providerName: string
  context: string
  theme: ProviderIconTheme
  revision: string
}

// One request per context/provider/revision/theme is shared by every icon on
// screen. Only definitive answers are kept: a transport error or a stale
// reply is retried by the next lookup.
const lookups = new Map<string, Promise<string | null>>()

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
  const existing = lookups.get(key)
  if (existing) return existing

  const pending = providerIcon({
    providerName: lookup.providerName,
    context: lookup.context,
    theme: lookup.theme,
    expectedRevision: lookup.revision,
  }).then(
    (reply) => {
      if ("stale" in reply) {
        lookups.delete(key)
        return null
      }
      return "dataUrl" in reply ? reply.dataUrl : null
    },
    () => {
      lookups.delete(key)
      return null
    },
  )
  lookups.set(key, pending)
  while (lookups.size > MAX_ENTRIES) {
    const oldest = lookups.keys().next().value
    if (oldest === undefined) break
    lookups.delete(oldest)
  }
  return pending
}

export function clearProviderIconLoader(): void {
  lookups.clear()
}
