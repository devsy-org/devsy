import { mainLog } from "./logging.js"

export interface Workspace {
  id: string
  lastUsed?: string
  status?: string
  [key: string]: unknown
}

export interface Provider {
  name: string
  icon?: string
  iconDark?: string
  iconRevision?: string
  state?: { singleMachine?: boolean; [key: string]: unknown }
  [key: string]: unknown
}

export interface Machine {
  id: string
  [key: string]: unknown
}

export interface Context {
  name: string
  options?: Record<string, string>
}

export class DaemonState {
  private workspaces = new Map<string, Workspace>()
  private providers = new Map<string, Provider>()
  private providerWriteRevision = 0
  private providerIconRevision = 0
  private machines = new Map<string, Machine>()
  private contexts: Context[] = []
  private activeContext = ""
  private workspaceListeners = new Set<() => void>()

  updateWorkspaces(list: Workspace[]): boolean {
    const merged = list.map((w) => {
      const existing = this.workspaces.get(w.id)
      if (
        existing &&
        typeof w.status === "undefined" &&
        typeof existing.status !== "undefined"
      ) {
        return { ...w, status: existing.status }
      }
      return w
    })
    const newMap = new Map(merged.map((w) => [w.id, w]))
    if (this.mapsEqual(this.workspaces, newMap)) return false
    this.workspaces = newMap
    this.notifyWorkspaceListeners()
    return true
  }

  updateWorkspaceStatus(id: string, status: string): boolean {
    const workspace = this.workspaces.get(id)
    if (!workspace || workspace.status === status) return false
    this.workspaces.set(id, { ...workspace, status })
    this.notifyWorkspaceListeners()
    return true
  }

  onWorkspacesChange(listener: () => void): () => void {
    this.workspaceListeners.add(listener)
    return () => this.workspaceListeners.delete(listener)
  }

  providerReadRevision(): number {
    return this.providerWriteRevision
  }

  providerIconReadRevision(): number {
    return this.providerIconRevision
  }

  updateProviders(
    list: Provider[],
    readRevision = this.providerWriteRevision,
  ): boolean {
    if (readRevision < this.providerWriteRevision) return false
    const newMap = new Map(list.map((p) => [p.name, p]))
    if (this.mapsEqual(this.providers, newMap)) return false
    if (
      this.providers.size !== newMap.size ||
      list.some(
        (provider) =>
          !this.providers.has(provider.name) ||
          this.providers.get(provider.name)?.iconRevision !==
            provider.iconRevision,
      )
    )
      this.providerIconRevision++
    this.providers = newMap
    return true
  }

  confirmProviderSingleMachine(name: string, singleMachine: boolean): boolean {
    // Invalidate provider reads that started before the successful CLI write,
    // even when the provider is not in the cache or already has this value.
    this.providerWriteRevision++
    const provider = this.providers.get(name)
    if (!provider || provider.state?.singleMachine === singleMachine)
      return false
    this.providers.set(name, {
      ...provider,
      state: { ...provider.state, singleMachine },
    })
    return true
  }

  updateMachines(list: Machine[]): boolean {
    const newMap = new Map(list.map((m) => [m.id, m]))
    if (this.mapsEqual(this.machines, newMap)) return false
    this.machines = newMap
    return true
  }

  updateContexts(contexts: Context[], active: string): boolean {
    const contextsJson = JSON.stringify(contexts)
    const currentJson = JSON.stringify(this.contexts)
    if (contextsJson === currentJson && active === this.activeContext)
      return false
    if (active !== this.activeContext) this.providerIconRevision++
    this.contexts = contexts
    this.activeContext = active
    return true
  }

  workspaceList(): Workspace[] {
    return [...this.workspaces.values()].sort((a, b) =>
      (b.lastUsed ?? "").localeCompare(a.lastUsed ?? ""),
    )
  }

  providerList(): Provider[] {
    return [...this.providers.values()].sort((a, b) =>
      a.name.localeCompare(b.name),
    )
  }

  machineList(): Machine[] {
    return [...this.machines.values()].sort((a, b) => a.id.localeCompare(b.id))
  }

  contextList(): { contexts: Context[]; activeContext: string } {
    return { contexts: [...this.contexts], activeContext: this.activeContext }
  }

  // currentContext returns the active context, defaulting to "default" only
  // when the watcher has yet to populate state.
  currentContext(): string {
    return this.activeContext || "default"
  }

  // workspaceContext resolves the context name for a workspace. When the
  // workspace is unknown in state (e.g. workspace_up runs BEFORE the
  // watcher's first poll picks up the new workspace) the active context is
  // used instead of a hard-coded "default", so creation logs land alongside
  // the workspace they're describing rather than orphaned under `default/`.
  // Both fallback branches emit a main warning so log misrouting is visible.
  workspaceContext(workspaceId: string): string {
    const ws = this.workspaces.get(workspaceId)
    if (!ws) {
      const active = this.currentContext()
      mainLog.warn(
        `[state] workspaceContext: workspace ${workspaceId} not found in state; falling back to active context %q`,
        active,
      )
      return active
    }
    if (typeof ws.context !== "string" || ws.context === "") {
      const active = this.currentContext()
      mainLog.warn(
        `[state] workspaceContext: workspace ${workspaceId} has no context field; falling back to active context %q`,
        active,
      )
      return active
    }
    return ws.context
  }

  private mapsEqual<K, V>(a: Map<K, V>, b: Map<K, V>): boolean {
    if (a.size !== b.size) return false
    for (const [key, val] of a) {
      if (!b.has(key) || JSON.stringify(val) !== JSON.stringify(b.get(key)))
        return false
    }
    return true
  }

  private notifyWorkspaceListeners(): void {
    for (const listener of this.workspaceListeners) {
      try {
        listener()
      } catch (error) {
        mainLog.error("[state] workspace listener failed:", error)
      }
    }
  }
}
