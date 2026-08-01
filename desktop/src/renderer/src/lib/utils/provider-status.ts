import type { Provider, ProviderJob } from "$lib/types/index.js"

export type ProviderStatusKind = "ready" | "busy" | "failed" | "uninitialized"

export interface ProviderStatus {
  kind: ProviderStatusKind
  label: string
  /** Present when kind is "failed". */
  error?: string
}

const PHASE_LABELS: Record<string, string> = {
  installing_provider: "installing…",
  resolving_options: "resolving options…",
  running_init: "initializing…",
}

const ACTIVITY_LABELS: Record<ProviderJob["activity"], string> = {
  installing: "installing…",
  initializing: "initializing…",
  updating: "updating…",
}

/**
 * Collapse the two lifecycle axes into one thing to render.
 *
 * An active job wins over the persisted flag: `initialized` is false while a
 * provider is still installing, and showing "not initialized" then is the bug
 * this exists to prevent. A recorded failure also wins, so a provider that
 * failed to initialize reads as failed rather than merely not-initialized.
 */
export function providerStatus(
  provider: Provider,
  job?: ProviderJob,
): ProviderStatus {
  if (job?.error) {
    return { kind: "failed", label: "failed", error: job.error }
  }
  if (job) {
    const label =
      (job.phase && PHASE_LABELS[job.phase]) ?? ACTIVITY_LABELS[job.activity]
    return { kind: "busy", label }
  }
  if (provider.state?.initialized) {
    return { kind: "ready", label: "initialized" }
  }
  return { kind: "uninitialized", label: "not initialized" }
}
