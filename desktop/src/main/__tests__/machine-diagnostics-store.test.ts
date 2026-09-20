import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { describe, expect, it } from "vitest"
import { MachineDiagnosticsStore } from "../machine-diagnostics-store.js"

const response = (sequence: number) => ({
  schemaVersion: 1,
  machine: { id: "machine", context: "default", provider: "test", state: "Running" },
  source: { availability: "available" as const, freshness: "fresh" as const },
  daemon: { sessionId: "session", state: "running" as const, health: "healthy" as const, startedAt: "2026-09-19T00:00:00Z", updatedAt: "2026-09-19T00:00:00Z", workspaceCount: 1 },
  events: [{ schemaVersion: 1, sessionId: "session", sequence, timestamp: `2026-09-19T00:00:0${sequence}Z`, level: "info" as const, type: "daemon.started", message: "started" }],
  cursor: { next: `cursor-${sequence}`, state: "ok" as const },
})

describe("MachineDiagnosticsStore", () => {
  it("retains the last daemon snapshot and cursor when the remote reader fails", () => {
    const store = new MachineDiagnosticsStore(mkdtempSync(join(tmpdir(), "devsy-diagnostics-")))
    const key = { context: "default", machineId: "machine" }
    const good = store.merge(key, response(1))
    const failed = store.merge(key, { ...response(2), daemon: undefined, events: [], source: { availability: "permission_denied", freshness: "unknown", message: "Access denied" }, cursor: { state: "none" } })
    expect(failed.response.daemon).toEqual(good.response.daemon)
    expect(failed.cursor).toBe(good.cursor)
    expect(failed.lastSuccessfulCollectionAt).toBe(good.lastSuccessfulCollectionAt)
    expect(failed.lastCollectionError).toBe("Access denied")
    expect(failed.response.source.freshness).toBe("stale")
    expect(store.merge(key, response(2)).lastCollectionError).toBeUndefined()
  })
  it("deduplicates remote events and retains them after a stop", () => {
    const store = new MachineDiagnosticsStore(mkdtempSync(join(tmpdir(), "devsy-diagnostics-")))
    const key = { context: "default", machineId: "machine" }
    store.merge(key, response(1))
    const cache = store.merge(key, response(1))
    expect(cache.events).toHaveLength(1)
    expect(cache.cursor).toBe("cursor-1")
    expect(store.markStopped(key)?.response.source.availability).toBe("machine_stopped")
    expect(store.get(key)?.events).toHaveLength(1)
  })

  it("rejects path traversal in keys", () => {
    const store = new MachineDiagnosticsStore(mkdtempSync(join(tmpdir(), "devsy-diagnostics-")))
    expect(() => store.get({ context: "../other", machineId: "machine" })).toThrow("invalid diagnostics key")
  })

  it("clears a stale cursor after a remote session reset", () => {
    const store = new MachineDiagnosticsStore(mkdtempSync(join(tmpdir(), "devsy-diagnostics-")))
    const key = { context: "default", machineId: "machine" }
    store.merge(key, response(1))
    const reset = { ...response(2), cursor: { state: "reset" as const, reason: "session_changed" } }
    const cache = store.merge(key, reset)
    expect(cache.cursor).toBeUndefined()
    expect(cache.lastCollectionError).toBeUndefined()
  })
})
