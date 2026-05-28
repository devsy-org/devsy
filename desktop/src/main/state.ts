export interface Workspace {
  id: string
  lastUsed?: string
  [key: string]: unknown
}

export interface Provider {
  name: string
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
  private machines = new Map<string, Machine>()
  private contexts: Context[] = []
  private activeContext = ""

  updateWorkspaces(list: Workspace[]): boolean {
    const newMap = new Map(list.map((w) => [w.id, w]))
    if (this.mapsEqual(this.workspaces, newMap)) return false
    this.workspaces = newMap
    return true
  }

  updateProviders(list: Provider[]): boolean {
    const newMap = new Map(list.map((p) => [p.name, p]))
    if (this.mapsEqual(this.providers, newMap)) return false
    this.providers = newMap
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
  // Both fallback branches emit a console.warn so log misrouting is visible.
  workspaceContext(workspaceId: string): string {
    const ws = this.workspaces.get(workspaceId)
    if (!ws) {
      const active = this.currentContext()
      console.warn(
        `[state] workspaceContext: workspace ${workspaceId} not found in state; falling back to active context %q`,
        active,
      )
      return active
    }
    if (typeof ws.context !== "string" || ws.context === "") {
      const active = this.currentContext()
      console.warn(
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
}
