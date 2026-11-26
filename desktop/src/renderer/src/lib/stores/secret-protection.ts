import { writable } from "svelte/store"
import { secretProtectionStatus as loadStatus } from "$lib/ipc/commands.js"
import type { SecretProtectionStatus } from "$lib/types/index.js"

// Only metadata belongs here. Credentials remain local to operation dialogs.
export const secretProtectionStatus = writable<SecretProtectionStatus | null>(
  null,
)
export const secretProtectionLoading = writable(false)
export const secretProtectionError = writable<string | null>(null)
let requestGeneration = 0

export async function refreshSecretProtection(): Promise<void> {
  const generation = ++requestGeneration
  secretProtectionLoading.set(true)
  try {
    const status = await loadStatus()
    if (generation !== requestGeneration) return
    secretProtectionStatus.set(status)
    secretProtectionError.set(null)
  } catch {
    if (generation !== requestGeneration) return
    secretProtectionError.set("Unable to load secret security status.")
  } finally {
    if (generation === requestGeneration) secretProtectionLoading.set(false)
  }
}

export async function initSecretProtection(): Promise<void> {
  await refreshSecretProtection()
}
