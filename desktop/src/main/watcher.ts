import { existsSync } from "node:fs"
import { homedir } from "node:os"
import { join } from "node:path"
import { watch } from "chokidar"
import type { BrowserWindow } from "electron"
import type { CliRunner } from "./cli.js"
import type { DaemonClient } from "./daemon-client.js"
import type { DaemonState } from "./state.js"

interface WatcherDeps {
  cli: CliRunner
  daemon?: DaemonClient
  state: DaemonState
  getMainWindow: () => BrowserWindow | null
}

interface ContextEntry {
  name: string
  default?: boolean
}

export interface ProviderEntry {
  config: {
    name?: string
    version?: string
    icon?: string
    description?: string
    source?: Record<string, unknown>
    options?: Record<string, unknown>
    optionGroups?: unknown[]
  }
  state?: { initialized?: boolean }
  default?: boolean
}

export function parseProviderEntries(raw: Record<string, ProviderEntry>) {
  return Object.values(raw).map((entry) => ({
    name: entry.config.name ?? "",
    version: entry.config.version ?? "",
    icon: entry.config.icon ?? "",
    description: entry.config.description ?? "",
    source: entry.config.source ?? {},
    options: entry.config.options ?? {},
    optionGroups: entry.config.optionGroups ?? [],
    isDefault: entry.default ?? false,
    state: {
      initialized: entry.state?.initialized ?? false,
      singleMachine: false,
    },
  }))
}

export class Watcher {
  private pollTimer: ReturnType<typeof setInterval> | null = null
  private fsWatcher: ReturnType<typeof watch> | null = null

  constructor(private deps: WatcherDeps) {}

  start(): void {
    // Poll every 3 seconds
    this.pollTimer = setInterval(() => this.pollOnce(), 3000)

    // Watch ~/.devsy/ for filesystem changes
    const devsyDir = join(homedir(), ".devsy")
    if (existsSync(devsyDir)) {
      this.fsWatcher = watch(devsyDir, {
        ignoreInitial: true,
        awaitWriteFinish: { stabilityThreshold: 500 },
      })
      this.fsWatcher.on("all", () => this.pollOnce())
    }

    // Initial poll
    this.pollOnce()
  }

  stop(): void {
    if (this.pollTimer) {
      clearInterval(this.pollTimer)
      this.pollTimer = null
    }
    if (this.fsWatcher) {
      this.fsWatcher.close()
      this.fsWatcher = null
    }
  }

  private async pollOnce(): Promise<void> {
    await Promise.allSettled([
      this.pollWorkspaces(),
      this.pollProviders(),
      this.pollMachines(),
      this.pollContexts(),
    ])
  }

  private async pollWorkspaces(): Promise<void> {
    try {
      const workspaces = this.deps.daemon
        ? await this.deps.daemon.listWorkspaces<unknown[]>()
        : await this.deps.cli.run<unknown[]>(["list", "--skip-pro"])
      const changed = this.deps.state.updateWorkspaces(workspaces as any[])
      if (changed) {
        this.send("workspaces-changed", {
          workspaces: this.deps.state.workspaceList(),
        })
      }
    } catch {
      // Silently ignore poll failures
    }
  }

  private async pollProviders(): Promise<void> {
    try {
      const raw = this.deps.daemon
        ? await this.deps.daemon.listProviders<Record<string, ProviderEntry>>()
        : await this.deps.cli.run<Record<string, ProviderEntry>>([
            "provider",
            "list",
          ])
      const providers = parseProviderEntries(raw)
      const changed = this.deps.state.updateProviders(providers as any[])
      if (changed) {
        this.send("providers-changed", {
          providers: this.deps.state.providerList(),
        })
      }
    } catch {
      // Silently ignore poll failures
    }
  }

  private async pollMachines(): Promise<void> {
    try {
      const machines = this.deps.daemon
        ? await this.deps.daemon.listMachines<unknown[]>()
        : await this.deps.cli.run<unknown[]>(["machine", "list"])
      const changed = this.deps.state.updateMachines(machines as any[])
      if (changed) {
        this.send("machines-changed", {
          machines: this.deps.state.machineList(),
        })
      }
    } catch {
      // Silently ignore poll failures
    }
  }

  private async pollContexts(): Promise<void> {
    try {
      const entries = this.deps.daemon
        ? await this.deps.daemon.listContexts<ContextEntry[]>()
        : await this.deps.cli.run<ContextEntry[]>(["context", "list"])
      const active = entries.find((e) => e.default)?.name ?? ""
      const contexts = entries.map((e) => ({ name: e.name }))
      const changed = this.deps.state.updateContexts(contexts, active)
      if (changed) {
        const { contexts: ctxList, activeContext } =
          this.deps.state.contextList()
        this.send("contexts-changed", { contexts: ctxList, activeContext })
      }
    } catch {
      // Silently ignore poll failures
    }
  }

  private send(channel: string, payload: unknown): void {
    const win = this.deps.getMainWindow()
    if (win && !win.isDestroyed()) {
      win.webContents.send(channel, payload)
    }
  }
}
