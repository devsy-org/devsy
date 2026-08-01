/**
 * Tracks in-flight provider install/init/update work in the main process.
 *
 * The persisted `initialized` flag answers "is this provider usable", which
 * is false both for a provider the user never initialized and for one being
 * installed right now. Those render very differently, so the transient half
 * of the lifecycle lives here rather than being inferred from disk state.
 *
 * Main-process ownership is deliberate: a renderer-local pending set is lost
 * when the wizard closes, the user navigates away, or the window reloads,
 * which is exactly when a multi-second install is still running.
 */

/** Phases emitted by the Go provider pipeline. */
export type ProviderPhase =
  | "installing_provider"
  | "resolving_options"
  | "running_init"
  | "ready"
  | "failed"

export type ProviderActivity = "installing" | "initializing" | "updating"

export interface ProviderJob {
  activity: ProviderActivity
  phase?: ProviderPhase
  /** Set when the job ended in failure; the job is retained so the UI can show why. */
  error?: string
}

export class ProviderJobs {
  private jobs = new Map<string, ProviderJob>()
  // Lets an in-flight finish() tell whether it still owns the entry.
  private generations = new Map<string, number>()
  private lastGeneration = 0
  private listeners = new Set<() => void>()

  /**
   * Register a callback fired after every mutation, so a phase transition
   * reaches the renderer without waiting for the next 3s disk poll.
   */
  onChange(listener: () => void): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  private emit(): void {
    for (const listener of this.listeners) listener()
  }

  /** Begin tracking work on a provider, clearing any previous failure. */
  start(name: string, activity: ProviderActivity): void {
    this.jobs.set(name, { activity })
    this.generations.set(name, ++this.lastGeneration)
    this.emit()
  }

  /**
   * Record a phase transition. Ignored when no job is active, so a stray
   * event can't resurrect a provider the UI already considers settled.
   */
  report(name: string, phase: ProviderPhase, error?: string): void {
    const job = this.jobs.get(name)
    if (!job) return
    if (phase === "failed") {
      this.jobs.set(name, { ...job, phase, error: error ?? "failed" })
    } else {
      this.jobs.set(name, { ...job, phase })
    }
    this.emit()
  }

  /** Stop tracking a provider, discarding any recorded failure. */
  clear(name: string): void {
    this.generations.delete(name)
    if (this.jobs.delete(name)) this.emit()
  }

  /**
   * Finish a job. A failure is retained so the UI can explain it; success
   * drops the entry and lets the persisted `initialized` flag speak.
   *
   * On success the caller must have refreshed the provider list first:
   * clearing the job while the list still shows the pre-init `initialized:
   * false` would expose the same red badge this class exists to prevent,
   * until the next poll caught up.
   */
  async finish(name: string, error?: string): Promise<void> {
    if (error) {
      const job = this.jobs.get(name)
      if (!job) return
      this.jobs.set(name, {
        activity: job.activity,
        phase: "failed",
        error,
      })
      this.emit()
      return
    }
    const job = this.jobs.get(name)
    if (!job) return
    // A recorded failure survives a later success-shaped finish.
    if (job.error) return

    const generation = this.generations.get(name)
    await this.refresh?.()
    // refresh is a CLI round-trip; a newer job may own the entry by now.
    if (this.generations.get(name) !== generation) return

    this.jobs.delete(name)
    this.generations.delete(name)
    this.emit()
  }

  /**
   * Supplies a way to re-read provider state from disk, so a finished job
   * isn't cleared before the list reflects what the command just wrote.
   */
  setRefresh(refresh: () => Promise<void>): void {
    this.refresh = refresh
  }

  private refresh?: () => Promise<void>

  get(name: string): ProviderJob | undefined {
    return this.jobs.get(name)
  }

  /** Snapshot for IPC, keyed by provider name. */
  snapshot(): Record<string, ProviderJob> {
    return Object.fromEntries(this.jobs)
  }
}
