import { readFileSync } from "node:fs"
import { dirname, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import { afterEach, describe, expect, it, vi } from "vitest"

const identityFixture = JSON.parse(
  readFileSync(
    resolve(
      dirname(fileURLToPath(import.meta.url)),
      "../../../../pkg/telemetry/testdata/identity.json",
    ),
    "utf8",
  ),
) as {
  version: number
  machine_vectors: Array<{ normalized_machine_id: string; distinct_id: string }>
  workspace_vectors: Array<{ workspace_uid: string; workspace_id: string }>
  override_vectors: Array<{
    override: string
    normalized_machine_id: string
    distinct_id: string
  }>
  failure_vectors: Array<{
    fallback_seed: string | null
    random_error: boolean
    distinct_id: string | null
  }>
}

const captured = vi.hoisted(() =>
  vi.fn((_event: { properties: Record<string, unknown> }) => undefined),
)
vi.mock("posthog-node", () => ({
  PostHog: class {
    capture = captured
    shutdown = vi.fn(async () => undefined)
  },
}))
vi.mock("electron", () => ({ app: { getVersion: () => "test" } }))
vi.mock("../logging.js", () => ({ mainLog: { warn: vi.fn() } }))

async function loadAnalytics(machineId: string, override?: string) {
  vi.resetModules()
  vi.stubEnv("DEVSY_TELEMETRY_DISTINCT_ID", override)
  const readMachineId = vi.fn(() => machineId)
  vi.doMock("../machine-id.js", () => ({ machineIdSync: readMachineId }))
  const analytics = await import("../analytics.js")
  return { ...analytics, readMachineId }
}

afterEach(() => {
  vi.unstubAllEnvs()
  vi.doUnmock("../machine-id.js")
  vi.unstubAllGlobals()
  captured.mockClear()
  vi.resetModules()
})

describe("analytics identity", () => {
  it("matches the shared machine, workspace, override, and failure vectors", async () => {
    expect(identityFixture.version).toBe(1)
    for (const vector of identityFixture.machine_vectors) {
      const analytics = await loadAnalytics(vector.normalized_machine_id)
      expect(analytics.getAnalyticsDistinctId()).toBe(vector.distinct_id)
    }
    for (const vector of identityFixture.workspace_vectors) {
      const analytics = await loadAnalytics("unused")
      expect(analytics.hashWorkspaceUid(vector.workspace_uid)).toBe(
        vector.workspace_id,
      )
    }
    for (const vector of identityFixture.override_vectors) {
      const analytics = await loadAnalytics(
        vector.normalized_machine_id,
        vector.override,
      )
      expect(analytics.getAnalyticsDistinctId()).toBe(vector.distinct_id)
      if (vector.override)
        expect(analytics.readMachineId).not.toHaveBeenCalled()
    }
    for (const vector of identityFixture.failure_vectors) {
      const analytics = await loadAnalytics(vector.fallback_seed ?? "")
      expect(analytics.getAnalyticsDistinctId() || null).toBe(
        vector.distinct_id,
      )
    }
  })

  it("preserves an already-derived override without reading the machine ID", async () => {
    const analytics = await loadAnalytics("ignored", "explicit-derived-id")
    expect(analytics.getAnalyticsDistinctId()).toBe("explicit-derived-id")
    expect(analytics.readMachineId).not.toHaveBeenCalled()
  })

  it("omits identity if native and secure random identity are unavailable", async () => {
    const analytics = await loadAnalytics("")
    expect(analytics.getAnalyticsDistinctId()).toBe("")
    expect(analytics.getAnalyticsDistinctId()).toBe("")
    expect(analytics.readMachineId).toHaveBeenCalledTimes(1)
  })

  it("domain-separates full machine and workspace identities", async () => {
    const analytics = await loadAnalytics("same-synthetic-value")
    const machine = analytics.getAnalyticsDistinctId()
    const workspace = analytics.hashWorkspaceUid("same-synthetic-value")
    expect(machine).toMatch(/^[0-9a-f]{64}$/)
    expect(workspace).toMatch(/^[0-9a-f]{64}$/)
    expect(workspace).not.toBe(machine)
  })

  it("exports only controlled telemetry properties", async () => {
    vi.stubEnv("DEVSY_DISABLE_TELEMETRY", "false")
    vi.stubGlobal("__DEVSY_POSTHOG_API_KEY__", "synthetic-key")
    const analytics = await loadAnalytics("synthetic-machine")
    analytics.initAnalytics()
    analytics.trackEvent("workspace_create", {
      provider: "host-secret",
      ide: "vscode",
      duration_ms: 12,
      message: "private-message",
      path: "/private/path",
      workspace_id: "not-a-digest",
    })
    expect(captured).toHaveBeenCalledWith(
      expect.objectContaining({
        event: "workspace_create",
        properties: expect.objectContaining({
          provider: "custom",
          ide: "vscode",
          duration_ms: 12,
          version: "test",
        }),
      }),
    )
    const properties = captured.mock.calls[0][0].properties
    expect(properties).not.toHaveProperty("message")
    expect(properties).not.toHaveProperty("path")
    expect(properties).not.toHaveProperty("workspace_id")
  })

  it("does not capture telemetry when opted out", async () => {
    vi.stubEnv("DEVSY_DISABLE_TELEMETRY", "true")
    vi.stubGlobal("__DEVSY_POSTHOG_API_KEY__", "synthetic-key")
    const analytics = await loadAnalytics("synthetic-machine")
    analytics.initAnalytics()
    analytics.trackEvent("workspace_failure", { error_code: "unlock_required" })
    expect(captured).not.toHaveBeenCalled()
  })

  it.each([
    ["unlock_required", "unlock_required"],
    ["ssh_host_key_failed", "ssh_host_key_failed"],
    ["cancelled", "canceled"],
    ["timeout", "deadline_exceeded"],
    ["private-error-details", "UNKNOWN"],
  ])("emits canonical error code for %s", async (input, expected) => {
    vi.stubEnv("DEVSY_DISABLE_TELEMETRY", "false")
    vi.stubGlobal("__DEVSY_POSTHOG_API_KEY__", "synthetic-key")
    const analytics = await loadAnalytics("synthetic-machine")
    analytics.initAnalytics()
    analytics.trackEvent("workspace_failure", {
      error_code: input,
      message: "private-error-details",
    })
    expect(captured).toHaveBeenCalledWith(
      expect.objectContaining({
        properties: expect.objectContaining({ error_code: expected }),
      }),
    )
    expect(captured.mock.calls[0][0].properties).not.toHaveProperty("message")
  })
})
