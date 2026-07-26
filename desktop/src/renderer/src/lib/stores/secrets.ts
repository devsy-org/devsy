import { writable } from "svelte/store"
import { secretList } from "$lib/ipc/commands.js"
import type { Secret } from "$lib/types/index.js"

export const secrets = writable<Secret[]>([])
export const secretsLoading = writable(true)

export async function refreshSecrets(): Promise<void> {
  try {
    secrets.set(await secretList())
  } catch {
    // IPC not available
  }
}

export async function initSecrets(): Promise<void> {
  secretsLoading.set(true)
  try {
    secrets.set(await secretList())
  } catch {
    // IPC not available
  } finally {
    secretsLoading.set(false)
  }
}
