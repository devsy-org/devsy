import { writable } from "svelte/store"
import { envList } from "$lib/ipc/commands.js"
import type { EnvVar } from "$lib/types/index.js"
import { extractErrorMessage } from "$lib/utils/error.js"

export const envVars = writable<EnvVar[]>([])
export const envLoading = writable(true)
export const envError = writable<string | null>(null)
let requestGeneration = 0

export async function refreshEnv(): Promise<void> {
  const generation = ++requestGeneration
  envError.set(null)
  try {
    const result = await envList()
    if (generation === requestGeneration) envVars.set(result)
  } catch (error) {
    if (generation === requestGeneration)
      envError.set(extractErrorMessage(error))
  }
}

export async function initEnv(): Promise<void> {
  envLoading.set(true)
  await refreshEnv()
  envLoading.set(false)
}
