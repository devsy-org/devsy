import { writable } from "svelte/store"
import { secretList } from "$lib/ipc/commands.js"
import type { Secret } from "$lib/types/index.js"

export const secrets = writable<Secret[]>([])
export const secretsLoading = writable(true)
export const secretsError = writable<string | null>(null)

export async function refreshSecrets(): Promise<void> {
  try {
    secrets.set(await secretList())
    secretsError.set(null)
  } catch (err) {
    secretsError.set(err instanceof Error ? err.message : String(err))
  }
}

export async function initSecrets(): Promise<void> {
  secretsLoading.set(true)
  try {
    secrets.set(await secretList())
    secretsError.set(null)
  } catch (err) {
    secretsError.set(err instanceof Error ? err.message : String(err))
  } finally {
    secretsLoading.set(false)
  }
}
