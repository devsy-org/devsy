import type { MachineDiagnosticsCache, MachineDiagnosticsResponse } from "../shared/machine-diagnostics-types.js"
import type { CliRunner } from "./cli.js"
import { MachineDiagnosticsStore, type MachineDiagnosticsKey } from "./machine-diagnostics-store.js"

function keyOf(key: MachineDiagnosticsKey): string {
  return JSON.stringify([key.context, key.machineId])
}

export class MachineDiagnosticsManager {
  private readonly inFlight = new Map<string, Promise<MachineDiagnosticsCache>>()
  private readonly invalidated = new Set<string>()

  constructor(
    private readonly cli: CliRunner,
    private readonly store: MachineDiagnosticsStore,
  ) {}

  getCached(key: MachineDiagnosticsKey): MachineDiagnosticsCache | null {
    return this.store.get(key)
  }

  refresh(key: MachineDiagnosticsKey): Promise<MachineDiagnosticsCache> {
    const id = keyOf(key)
    const active = this.inFlight.get(id)
    if (active) return active
    this.invalidated.delete(id)

    const request = this.collect(key).finally(() => {
      this.inFlight.delete(id)
      this.invalidated.delete(id)
    })
    this.inFlight.set(id, request)
    return request
  }

  markStopped(key: MachineDiagnosticsKey): MachineDiagnosticsCache | null {
    if (this.inFlight.has(keyOf(key))) this.invalidated.add(keyOf(key))
    return this.store.markStopped(key)
  }

  delete(key: MachineDiagnosticsKey): void {
    if (this.inFlight.has(keyOf(key))) this.invalidated.add(keyOf(key))
    this.store.delete(key)
  }

  private async collect(key: MachineDiagnosticsKey): Promise<MachineDiagnosticsCache> {
    const cached = this.store.get(key)
    const args = ["machine", "diagnostics", key.machineId, "--context", key.context, "--result-format", "json", "--limit", "200"]
    if (cached?.cursor) args.push("--after", cached.cursor)

    try {
      const response = JSON.parse(await this.cli.runRaw(args)) as MachineDiagnosticsResponse
      if (this.invalidated.has(keyOf(key))) throw new Error("Diagnostics collection superseded by a machine lifecycle change.")
      if (response.schemaVersion !== 1 || response.machine?.id !== key.machineId || response.machine?.context !== key.context || !response.source || !response.cursor) {
        throw new Error("Remote diagnostics returned an invalid or mismatched response.")
      }
      return this.store.merge(key, response)
    } catch (error) {
      if (this.invalidated.has(keyOf(key))) throw error
      const message = error instanceof Error ? error.message : "Remote diagnostics collection failed."
      const retained = this.store.recordFailure(key, message)
      if (retained) return retained
      throw error
    }
  }
}
