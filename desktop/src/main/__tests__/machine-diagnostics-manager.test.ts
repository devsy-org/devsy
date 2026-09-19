import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { describe, expect, it, vi } from "vitest"
import type { CliRunner } from "../cli.js"
import { MachineDiagnosticsManager } from "../machine-diagnostics-manager.js"
import { MachineDiagnosticsStore } from "../machine-diagnostics-store.js"

describe("MachineDiagnosticsManager", () => {
  it("coalesces concurrent refreshes for the same machine", async () => {
    const runRaw = vi.fn().mockResolvedValue(JSON.stringify({
      schemaVersion: 1,
      machine: { id: "machine", context: "default", provider: "test", state: "Running" },
      source: { availability: "available", freshness: "fresh" },
      events: [],
      cursor: { state: "none" },
    }))
    const manager = new MachineDiagnosticsManager(
      { runRaw } as unknown as CliRunner,
      new MachineDiagnosticsStore(mkdtempSync(join(tmpdir(), "devsy-diagnostics-"))),
    )
    const key = { context: "default", machineId: "machine" }
    const [first, second] = await Promise.all([manager.refresh(key), manager.refresh(key)])
    expect(runRaw).toHaveBeenCalledTimes(1)
    expect(runRaw.mock.calls[0][0]).toEqual(expect.arrayContaining(["--context", "default"]))
    expect(first).toEqual(second)
  })

  it("does not recreate a deleted cache when an older collection completes", async () => {
    let resolve!: (value: string) => void
    const runRaw = vi.fn(() => new Promise<string>((done) => { resolve = done }))
    const store = new MachineDiagnosticsStore(mkdtempSync(join(tmpdir(), "devsy-diagnostics-")))
    const manager = new MachineDiagnosticsManager({ runRaw } as unknown as CliRunner, store)
    const key = { context: "default", machineId: "machine" }
    const pending = manager.refresh(key)
    manager.delete(key)
    resolve(JSON.stringify({ schemaVersion: 1, machine: { id: "machine", context: "default" }, source: { availability: "available", freshness: "fresh" }, cursor: { state: "none" } }))
    await expect(pending).rejects.toThrow("superseded")
    expect(store.get(key)).toBeNull()
  })

  it("retains the last good snapshot after a collection failure", async () => {
    const success = JSON.stringify({ schemaVersion: 1, machine: { id: "machine", context: "default", provider: "test", state: "Running" }, source: { availability: "available", freshness: "fresh" }, events: [], cursor: { state: "none" } })
    const runRaw = vi.fn().mockResolvedValueOnce(success).mockRejectedValueOnce(new Error("network unavailable")).mockResolvedValueOnce(success)
    const manager = new MachineDiagnosticsManager({ runRaw } as unknown as CliRunner, new MachineDiagnosticsStore(mkdtempSync(join(tmpdir(), "devsy-diagnostics-"))))
    const key = { context: "default", machineId: "machine" }
    await manager.refresh(key)
    const retained = await manager.refresh(key)
    expect(retained.lastCollectionError).toBe("network unavailable")
    expect(retained.response.source.availability).toBe("available")
    const recovered = await manager.refresh(key)
    expect(recovered.lastCollectionError).toBeUndefined()
  })
})
