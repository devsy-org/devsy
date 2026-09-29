import { appNavigationApplied } from "$lib/ipc/commands.js"
import { currentRoute, goto } from "$lib/router.js"
import { isAppNavigationRequest } from "$shared/app-route.js"

let latestRequestId = 0

export async function applyAppNavigationRequest(
  value: unknown,
): Promise<boolean> {
  if (!isAppNavigationRequest(value)) return false
  if (value.id < latestRequestId) return false
  latestRequestId = value.id
  await goto(value.route)
  if (latestRequestId !== value.id || currentRoute() !== value.route)
    return false
  void appNavigationApplied(value).catch((error) => {
    console.warn("[Devsy] navigation acknowledgment failed:", error)
  })
  return true
}
