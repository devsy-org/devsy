import { createHmac } from "node:crypto"
import { homedir, platform, arch } from "node:os"
import { PostHog } from "posthog-node"
import { app } from "electron"
import { machineIdSync } from "./machine-id.js"

declare const __DEVSY_POSTHOG_API_KEY__: string | undefined
// `typeof` guard keeps the module loadable under vitest, which doesn't
// apply Vite's `define` replacement at test time.
const DEVSY_POSTHOG_API_KEY =
  typeof __DEVSY_POSTHOG_API_KEY__ === "string" ? __DEVSY_POSTHOG_API_KEY__ : ""
const POSTHOG_HOST = "https://us.i.posthog.com"

let client: PostHog | null = null
let distinctId = ""

export function getAnalyticsDistinctId(): string {
  return distinctId || getDistinctId()
}

function getDistinctId(): string {
  const id = machineIdSync()
  const home = homedir()
  const mac = createHmac("sha256", id)
  mac.update(home)
  return mac.digest("hex")
}

function isTelemetryDisabled(): boolean {
  return process.env.DEVSY_DISABLE_TELEMETRY === "true"
}

export function initAnalytics(): void {
  if (isTelemetryDisabled()) return
  if (!DEVSY_POSTHOG_API_KEY) {
    console.warn("[telemetry] PostHog API key not configured; analytics disabled")
    return
  }

  distinctId = getDistinctId()
  client = new PostHog(DEVSY_POSTHOG_API_KEY, {
    host: POSTHOG_HOST,
    flushAt: 20,
    flushInterval: 30_000,
  })
}

export function trackEvent(
  name: string,
  properties?: Record<string, unknown>,
): void {
  if (!client || isTelemetryDisabled()) return

  client.capture({
    distinctId,
    event: name,
    properties: {
      app_version: app.getVersion(),
      os_name: platform(),
      os_arch: arch(),
      ...properties,
    },
  })
}

export async function shutdownAnalytics(): Promise<void> {
  if (!client) return
  await client.shutdown()
  client = null
}
