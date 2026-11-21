import { createHmac } from "node:crypto"
import { arch, platform } from "node:os"
import { app } from "electron"
import { PostHog } from "posthog-node"
import { mainLog } from "./logging.js"
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

export function hashWorkspaceRef(workspaceId: string): string {
  if (!client) return ""
  return createHmac("sha256", getAnalyticsDistinctId())
    .update(workspaceId)
    .digest("hex")
    .slice(0, 16)
}

export function hashWorkspaceUid(uid: string): string {
  return createHmac("sha256", "devsy-telemetry-v1")
    .update("workspace\0")
    .update(uid)
    .digest("hex")
}

let cachedDistinctId: string | undefined

function getDistinctId(): string {
  if (cachedDistinctId !== undefined) return cachedDistinctId
  const override = process.env.DEVSY_TELEMETRY_DISTINCT_ID
  if (override) {
    cachedDistinctId = override
    return cachedDistinctId
  }
  const id = machineIdSync()
  if (!id) {
    cachedDistinctId = ""
    return cachedDistinctId
  }
  cachedDistinctId = createHmac("sha256", "devsy-telemetry-v1")
    .update("machine\0")
    .update(id)
    .digest("hex")
  return cachedDistinctId
}

function isTelemetryDisabled(): boolean {
  return process.env.DEVSY_DISABLE_TELEMETRY === "true"
}

export function initAnalytics(): void {
  if (isTelemetryDisabled()) return
  if (!DEVSY_POSTHOG_API_KEY) {
    mainLog.warn("[telemetry] analytics disabled: API key not configured")
    return
  }

  distinctId = getDistinctId()
  if (!distinctId) return
  client = new PostHog(DEVSY_POSTHOG_API_KEY, {
    host: POSTHOG_HOST,
    flushAt: 20,
    flushInterval: 30_000,
    disableGeoip: false,
    isServer: false,
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
      ...sanitizeAnalyticsProperties(properties),
      version: app.getVersion(),
      os_name: platform() === "win32" ? "windows" : platform(),
      os_arch: arch(),
      timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    },
  })
}

function sanitizeAnalyticsProperties(
  input?: Record<string, unknown>,
): Record<string, unknown> {
  if (!input) return {}
  const out: Record<string, unknown> = {}
  if (
    typeof input.workspace_id === "string" &&
    /^[0-9a-f]{64}$/.test(input.workspace_id)
  )
    out.workspace_id = input.workspace_id
  if (
    typeof input.duration_ms === "number" &&
    Number.isFinite(input.duration_ms) &&
    input.duration_ms >= 0
  )
    out.duration_ms = input.duration_ms
  const allowed: Record<string, string[]> = {
    provider: [
      "apple",
      "docker",
      "kubernetes",
      "microsandbox",
      "podman",
      "pro",
      "custom",
    ],
    source_type: ["git", "local", "image", "other"],
    ide: [
      "vscode",
      "vscode-insiders",
      "cursor",
      "windsurf",
      "jetbrains",
      "none",
    ],
    error_code: [
      "UNKNOWN",
      "PANIC",
      "RATE_LIMITED",
      "BUILD_FAILED_RECOVERABLE",
      "canceled",
      "deadline_exceeded",
      "docker_daemon_unreachable",
      "unlock_required",
      "unlock_failed",
      "secret_backend_unavailable",
      "secret_store_corrupt",
      "secret_not_found",
      "ssh_auth_failed",
      "ssh_host_key_failed",
      "ssh_unreachable",
      "ssh_command_failed",
    ],
  }
  for (const [key, values] of Object.entries(allowed)) {
    const value = input[key]
    if (typeof value !== "string") continue
    const normalized =
      key === "error_code"
        ? value === "cancelled"
          ? "canceled"
          : value === "timeout"
            ? "deadline_exceeded"
            : value
        : value
    if (values.includes(normalized)) out[key] = normalized
    else if (key === "provider") out[key] = "custom"
    else if (key === "error_code") out[key] = "UNKNOWN"
  }
  return out
}

export async function shutdownAnalytics(): Promise<void> {
  if (!client) return
  await client.shutdown()
  client = null
}
