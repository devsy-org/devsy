import { existsSync } from "node:fs"
import { homedir } from "node:os"
import { join } from "node:path"
import { watch } from "chokidar"
import type { BrowserWindow } from "electron"
import type { CliRunner } from "./cli.js"
import type { DaemonClient } from "./daemon-client.js"
import type { ProviderJobs } from "./provider-jobs.js"
import type { DaemonState } from "./state.js"
import type { WorkspaceJobs } from "./workspace-jobs.js"

interface WatcherDeps {
  cli: CliRunner
  daemon?: DaemonClient
  state: DaemonState
  getMainWindow: () => BrowserWindow | null
  providerJobs: ProviderJobs
  workspaceJobs: WorkspaceJobs
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
  state?: { initialized?: boolean; singleMachine?: boolean }
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
      singleMachine: entry.state?.singleMachine ?? false,
    },
  }))
}

export class Watcher {
  private pollTimer: ReturnType<typeof setInterval> | null = null
  private fsWatcher: ReturnType<typeof watch> | null = null
  private polling = false
  private pollQueued = false
  // Serializes pollProviders so a manual refreshProviders() can never
  // overlap a scheduled poll; each queued call is guaranteed a fresh read
  // that starts after it was requested.
  private providerPollChain: Promise<void> = Promise.resolve()
  // Same serialization for pollWorkspaces, so a manual refreshWorkspaces()
  // (e.g. after a delete finishes) can't overlap a scheduled poll.
  private workspacePollChain: Promise<void> = Promise.resolve()

  constructor(private deps: WatcherDeps) {}

  start(): Promise<void> {
    this.pollTimer = setInterval(() => this.schedulePoll(), 3000)

    const devsyDir = join(homedir(), ".devsy")
    if (existsSync(devsyDir)) {
      this.fsWatcher = watch(devsyDir, {
        ignoreInitial: true,
        ignored: /[\\/]logs[\\/]/,
        awaitWriteFinish: { stabilityThreshold: 500 },
      })
      this.fsWatcher.on("all", () => this.schedulePoll())
    }

    return this.pollOnce()
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

  private schedulePoll(): void {
    if (this.polling) {
      this.pollQueued = true
      return
    }
    this.pollOnce()
  }

  private async pollOnce(): Promise<void> {
    this.polling = true
    try {
      await Promise.allSettled([
        this.queueWorkspacePoll(),
        this.queueProviderPoll(),
        this.pollMachines(),
        this.pollContexts(),
      ])
    } finally {
      this.polling = false
      if (this.pollQueued) {
        this.pollQueued = false
        this.schedulePoll()
      }
    }
  }

  private async queryWithFallback<T>(
    daemonFn: (() => Promise<T>) | undefined,
    cliFn: () => Promise<T>,
  ): Promise<T> {
    if (daemonFn) {
      try {
        return await daemonFn()
      } catch {
        // Daemon unavailable, fall through to CLI
      }
    }
    return cliFn()
  }

  /** Re-read the workspace list from disk now, without waiting for the next scheduled poll. */
  async refreshWorkspaces(): Promise<void> {
    await this.queueWorkspacePoll()
  }

  private queueWorkspacePoll(): Promise<void> {
    const run = this.workspacePollChain.then(() => this.pollWorkspaces())
    this.workspacePollChain = run
    return run
  }

  private async pollWorkspaces(): Promise<void> {
    try {
      const workspaces = await this.queryWithFallback(
        this.deps.daemon
          ? () => this.deps.daemon!.listWorkspaces<unknown[]>()
          : undefined,
        () => this.deps.cli.run<unknown[]>(["workspace", "list", "--skip-pro"]),
      )
      const changed = this.deps.state.updateWorkspaces(workspaces as any[])
      if (changed) {
        this.broadcastWorkspaces()
      }
    } catch {
      // Silently ignore poll failures
    }
  }

  /**
   * Push the workspace list plus in-flight job state. Sent on one channel so
   * the two can't arrive out of order and show a deleted-but-still-listed
   * workspace as idle between the delete finishing and the list catching up.
   */
  broadcastWorkspaces(): void {
    this.send("workspaces-changed", {
      workspaces: this.deps.state.workspaceList(),
      jobs: this.deps.workspaceJobs.snapshot(),
    })
  }

  /** Re-read provider state from disk now, without waiting for the next scheduled poll. */
  async refreshProviders(): Promise<void> {
    await this.queueProviderPoll()
  }

  private queueProviderPoll(): Promise<void> {
    const run = this.providerPollChain.then(() => this.pollProviders())
    this.providerPollChain = run
    return run
  }

  private async pollProviders(): Promise<void> {
    try {
      const raw = await this.queryWithFallback(
        this.deps.daemon
          ? () =>
              this.deps.daemon!.listProviders<Record<string, ProviderEntry>>()
          : undefined,
        () =>
          this.deps.cli.run<Record<string, ProviderEntry>>([
            "provider",
            "list",
          ]),
      )
      const providers = parseProviderEntries(raw)
      const changed = this.deps.state.updateProviders(providers as any[])
      if (changed) {
        this.broadcastProviders()
      }
    } catch {
      // Silently ignore poll failures
    }
  }

  /**
   * Push the provider list plus in-flight job state. Sent on one channel so
   * the two can't arrive out of order and render a provider as idle-and-
   * uninitialized between an install finishing and its job clearing.
   */
  broadcastProviders(): void {
    this.send("providers-changed", {
      providers: this.deps.state.providerList(),
      jobs: this.deps.providerJobs.snapshot(),
    })
  }

  private async pollMachines(): Promise<void> {
    try {
      const machines = await this.queryWithFallback(
        this.deps.daemon
          ? () => this.deps.daemon!.listMachines<unknown[]>()
          : undefined,
        () => this.deps.cli.run<unknown[]>(["machine", "list"]),
      )
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
      const entries = await this.queryWithFallback(
        this.deps.daemon
          ? () => this.deps.daemon!.listContexts<ContextEntry[]>()
          : undefined,
        () => this.deps.cli.run<ContextEntry[]>(["context", "list"]),
      )
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
