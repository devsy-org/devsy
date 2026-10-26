import { writable } from "svelte/store"
import {
  getLinuxDesktopIntegration,
  installLinuxDesktopIntegration,
  removeLinuxDesktopIntegration,
} from "$lib/ipc/commands.js"
import type {
  LinuxDesktopIntegrationResult,
  LinuxDesktopIntegrationStatus,
} from "$shared/linux-desktop-integration.js"

export const integrationStatus = writable<LinuxDesktopIntegrationStatus | null>(
  null,
)
export const integrationBusy = writable(false)
export const integrationError = writable<string | undefined>(undefined)

/** The one-time prompt appears only where the install action can succeed. */
export function shouldPromptForIntegration(
  status: LinuxDesktopIntegrationStatus,
  dismissed: boolean,
): boolean {
  return (
    status.supported &&
    !status.installed &&
    !status.conflict &&
    !status.problem &&
    !dismissed
  )
}

export async function refreshIntegrationStatus(): Promise<LinuxDesktopIntegrationStatus | null> {
  try {
    const status = await getLinuxDesktopIntegration()
    integrationStatus.set(status)
    return status
  } catch (err) {
    console.warn("[integration] status failed:", err)
    return null
  }
}

async function runIntegrationAction(
  action: () => Promise<LinuxDesktopIntegrationResult>,
): Promise<void> {
  integrationBusy.set(true)
  integrationError.set(undefined)
  try {
    const result = await action()
    integrationStatus.set(result.status)
    integrationError.set(result.error)
  } catch (err) {
    integrationError.set(err instanceof Error ? err.message : String(err))
  } finally {
    integrationBusy.set(false)
  }
}

export function addToApplications(): Promise<void> {
  return runIntegrationAction(installLinuxDesktopIntegration)
}

export function removeFromApplications(): Promise<void> {
  return runIntegrationAction(removeLinuxDesktopIntegration)
}
