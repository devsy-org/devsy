/**
 * Tracks in-flight workspace delete operations in the main process, the
 * same way ProviderJobs tracks provider install/init work: workspace_delete
 * is fire-and-forget from the IPC handler's perspective (it returns as soon
 * as the CLI command is launched, so the log-streaming UI isn't blocked),
 * so nothing else records that a delete is running. Main-process ownership
 * means the "Deleting" badge survives navigating away from the list or
 * reloading the window while the delete is still in flight.
 */

export interface WorkspaceJob {
  activity: "deleting"
  /** Set when the job ended in failure; the job is retained so the UI can show why. */
  error?: string
}

export class WorkspaceJobs {
  private jobs = new Map<string, WorkspaceJob>()
  // Lets an in-flight finish() tell whether it still owns the entry.
  private generations = new Map<string, number>()
  private lastGeneration = 0
  private listeners = new Set<() => void>()
  private refresh?: () => Promise<void>

  /**
   * Register a callback fired after every mutation, so starting or
   * finishing a delete reaches the renderer without waiting for the next
   * disk poll.
   */
  onChange(listener: () => void): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  private emit(): void {
    for (const listener of this.listeners) listener()
  }

  /**
   * Begin tracking a delete, clearing any previous failure. Returns a
   * generation token the caller must pass to finish() so a stale exit
   * callback from an earlier delete of the same workspace can't land on
   * a retry that has already superseded it.
   */
  start(id: string): number {
    this.jobs.set(id, { activity: "deleting" })
    const generation = ++this.lastGeneration
    this.generations.set(id, generation)
    this.emit()
    return generation
  }

  /** Stop tracking a workspace, discarding any recorded failure. */
  clear(id: string): void {
    this.generations.delete(id)
    if (this.jobs.delete(id)) this.emit()
  }

  /**
   * Finish the job started under `generation`. A failure is retained so the
   * UI can explain it; success drops the entry once the workspace list has
   * caught up, so the card doesn't flash back to its pre-delete state for
   * one poll cycle. Either way, a generation mismatch means a retry has
   * already superseded this job, so the call is a no-op.
   */
  async finish(id: string, generation: number, error?: string): Promise<void> {
    if (this.generations.get(id) !== generation) return

    if (error) {
      this.jobs.set(id, { activity: "deleting", error })
      this.emit()
      return
    }
    const job = this.jobs.get(id)
    if (!job) return
    if (job.error) return

    await this.refresh?.()
    // refresh is a CLI round-trip; a newer job may own the entry by now.
    if (this.generations.get(id) !== generation) return

    this.jobs.delete(id)
    this.generations.delete(id)
    this.emit()
  }

  /**
   * Supplies a way to re-read the workspace list from disk, so a finished
   * job isn't cleared before the list reflects the deletion.
   */
  setRefresh(refresh: () => Promise<void>): void {
    this.refresh = refresh
  }

  get(id: string): WorkspaceJob | undefined {
    return this.jobs.get(id)
  }

  /** Snapshot for IPC, keyed by workspace id. */
  snapshot(): Record<string, WorkspaceJob> {
    return Object.fromEntries(this.jobs)
  }
}
