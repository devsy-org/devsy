import { writable } from "svelte/store"
import { envList } from "$lib/ipc/commands.js"
import type { EnvVar } from "$lib/types/index.js"

export const envVars = writable<EnvVar[]>([])
export const envLoading = writable(true)

export async function refreshEnv(): Promise<void> {
  try {
    envVars.set(await envList())
  } catch {
    // IPC not available
  }
}

export async function initEnv(): Promise<void> {
  envLoading.set(true)
  try {
    envVars.set(await envList())
  } catch {
    // IPC not available
  } finally {
    envLoading.set(false)
  }
}
