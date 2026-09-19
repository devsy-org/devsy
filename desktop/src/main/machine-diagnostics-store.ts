import { randomUUID } from "node:crypto"
import { existsSync, mkdirSync, readFileSync, renameSync, unlinkSync, writeFileSync } from "node:fs"
import { basename, join } from "node:path"
import type { MachineDiagnosticEvent, MachineDiagnosticsCache, MachineDiagnosticsResponse } from "../shared/machine-diagnostics-types.js"

export interface MachineDiagnosticsKey {
  context: string
  machineId: string
}

function safe(value: string): string {
  const clean = basename(value)
  if (!clean || clean === "." || clean === ".." || clean !== value) throw new Error("invalid diagnostics key")
  return clean
}

function eventKey(event: MachineDiagnosticEvent): string {
  return `${event.sessionId}:${event.sequence}`
}

export class MachineDiagnosticsStore {
  constructor(private readonly root: string) {}
  private path(key: MachineDiagnosticsKey): string {
    return join(this.root, "machines", safe(key.context), `${safe(key.machineId)}.json`)
  }

  get(key: MachineDiagnosticsKey): MachineDiagnosticsCache | null {
    const path = this.path(key)
    try {
      const cache = JSON.parse(readFileSync(path, "utf-8")) as MachineDiagnosticsCache
      if (cache.response?.schemaVersion !== 1 || !Array.isArray(cache.events)) return null
      return cache
    } catch {
      return null
    }
  }
  merge(key: MachineDiagnosticsKey, response: MachineDiagnosticsResponse): MachineDiagnosticsCache {
    if (response.source.availability !== "available") {
      const retained = this.get(key)
      if (retained) {
        retained.response.source = { ...response.source, freshness: "stale" }
        retained.response.machine = response.machine
        retained.lastAttemptAt = new Date().toISOString()
        retained.lastCollectionError = response.source.availability === "machine_stopped" ? undefined : (response.source.message ?? `Diagnostics are ${response.source.availability}.`)
        this.write(key, retained)
        return retained
      }
    }
    const existing = this.get(key)
    const events = [...(existing?.events ?? []), ...(response.events ?? [])]
    const unique = new Map(events.map(event => [eventKey(event), event]))
    const merged = [...unique.values()].sort((a, b) => a.timestamp.localeCompare(b.timestamp) || a.sequence - b.sequence).slice(-2000)
    const { events: _events, ...responseWithoutEvents } = response
    const now = new Date().toISOString()
    const cache: MachineDiagnosticsCache = {
      response: responseWithoutEvents,
      events: merged,
      cursor: response.cursor.state === "reset" ? undefined : (response.cursor.next ?? existing?.cursor),
      lastAttemptAt: now,
      lastSuccessfulCollectionAt: response.source.availability === "available" ? now : existing?.lastSuccessfulCollectionAt,
      historyGap: Boolean(existing?.historyGap || response.cursor.state === "gap"),
    }
    this.write(key, cache)
    return cache
  }
  markStopped(key: MachineDiagnosticsKey): MachineDiagnosticsCache | null {
    const cache = this.get(key)
    if (!cache) return null
    cache.response.source = { availability: "machine_stopped", freshness: "unknown" }
    cache.lastCollectionError = undefined
    this.write(key, cache)
    return cache
  }

  recordFailure(key: MachineDiagnosticsKey, message: string): MachineDiagnosticsCache | null {
    const cache = this.get(key)
    if (!cache) return null
    cache.lastAttemptAt = new Date().toISOString()
    cache.lastCollectionError = message
    cache.response.source.freshness = "stale"
    this.write(key, cache)
    return cache
  }

  delete(key: MachineDiagnosticsKey): void {
    const path = this.path(key)
    if (existsSync(path)) unlinkSync(path)
  }
  private write(key: MachineDiagnosticsKey, cache: MachineDiagnosticsCache): void {
    const path = this.path(key)
    mkdirSync(join(this.root, "machines", safe(key.context)), { recursive: true, mode: 0o700 })
    const tmp = `${path}.${randomUUID()}.tmp`
    try {
      writeFileSync(tmp, JSON.stringify(cache), { mode: 0o600 })
      renameSync(tmp, path)
    } finally {
      if (existsSync(tmp)) unlinkSync(tmp)
    }
  }
}
