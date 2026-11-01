export type ProviderIconTheme = "light" | "dark"

export interface ProviderIconRequest {
  providerName: string
  context: string
  theme: ProviderIconTheme
  /** The iconRevision the caller's provider list entry carries. */
  expectedRevision: string
}

/**
 * `dataUrl` and `missing` are mutually exclusive. `stale` means the installed
 * revision no longer matches `expectedRevision`: no image bytes are returned,
 * and `revision` is the newly observed revision to wait for in the provider list.
 */
export type ProviderIconReply =
  | { revision: string; dataUrl: string }
  | { revision: string; missing: true }
  | { revision: string; stale: true }

export const PROVIDER_ICON_MAX_BYTES = 1024 * 1024

export const PROVIDER_ICON_MIME_TYPES = [
  "image/png",
  "image/jpeg",
  "image/webp",
  "image/svg+xml",
] as const
