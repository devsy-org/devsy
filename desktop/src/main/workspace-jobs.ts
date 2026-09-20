import type { OperationStatus } from "../shared/cli-error.js"
import {
  type WorkspaceActivity,
  type WorkspaceJob,
  workspaceJobBusy,
} from "../shared/workspace-operation.js"
export type { WorkspaceJob } from "../shared/workspace-operation.js"

/** Main-owned actions are independent of the last observed runtime status. */
export class WorkspaceJobs {
  private jobs = new Map<string, WorkspaceJob>()
  private generations = new Map<string, number>()
  private lastGeneration = 0
  private listeners = new Set<() => void>()
  private refreshing = new Set<string>()
  private refresh?: (id: string, job: WorkspaceJob) => Promise<void>
  revision = 0

  onChange(listener: () => void): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }
  private emit(): void {
    this.revision++
    for (const listener of this.listeners) listener()
  }
  get epoch(): number {
    return this.lastGeneration
  }
  generation(id: string): number {
    return this.generations.get(id) ?? 0
  }
  owns(id: string, commandId: string): boolean {
    return this.jobs.get(id)?.commandId === commandId
  }

  start(
    id: string,
    activity: WorkspaceActivity = "deleting",
    commandId: string = crypto.randomUUID(),
  ): number {
    const current = this.jobs.get(id)
    const canInterrupt =
      (activity === "stopping" || activity === "deleting") &&
      (current?.activity === "starting" || current?.activity === "creating") &&
      current.state === "running"
    if (workspaceJobBusy(current) && !current?.error && !canInterrupt)
      throw new Error("A workspace operation is already in progress")
    const generation = ++this.lastGeneration
    this.generations.set(id, generation)
    this.jobs.set(id, {
      commandId,
      activity,
      state: "running",
      phase: "Preparing",
    })
    this.emit()
    return generation
  }
  phase(id: string, commandId: string, phase: string): void {
    const job = this.jobs.get(id)
    if (!job || job.commandId !== commandId || job.state !== "running") return
    this.jobs.set(id, { ...job, phase })
    this.emit()
  }
  progress(id: string, commandId: string, status: OperationStatus): void {
    const job = this.jobs.get(id)
    if (!job || job.commandId !== commandId || job.state !== "running") return
    // A child finishing does not finish its parent or the command.
    const phase =
      status.state === "started"
        ? status.step || status.phase.replaceAll("_", " ")
        : job.phase
    this.jobs.set(id, { ...job, status, phase })
    this.emit()
  }
  clear(id: string): void {
    this.generations.set(id, ++this.lastGeneration)
    if (this.jobs.delete(id)) this.emit()
  }
  async finish(id: string, generation: number, error?: string): Promise<void> {
    const job = this.jobs.get(id)
    if (!job || this.generation(id) !== generation || job.state !== "running")
      return
    this.generations.set(id, ++this.lastGeneration) // invalidate pre-completion polls
    this.jobs.set(id, {
      ...job,
      state: "reconciling",
      phase:
        job.activity === "deleting" ? "Refreshing list" : "Refreshing status",
      error,
    })
    this.emit()
    await this.retryRefresh(id)
  }
  async retryRefresh(id: string): Promise<void> {
    const job = this.jobs.get(id)
    if (
      !job ||
      job.state !== "reconciling" ||
      this.refreshing.has(job.commandId)
    )
      return
    this.refreshing.add(job.commandId)
    try {
      await this.refresh?.(id, job)
      if (this.jobs.get(id) !== job) return
      this.jobs.set(id, {
        ...job,
        state: job.error ? "failed" : "succeeded",
        phase: job.error ? "See workspace logs for details" : "",
        refreshError: undefined,
      })
    } catch (error) {
      if (this.jobs.get(id) !== job) return
      this.jobs.set(id, {
        ...job,
        refreshError: error instanceof Error ? error.message : String(error),
      })
    } finally {
      this.refreshing.delete(job.commandId)
      this.emit()
    }
  }
  setRefresh(refresh: (id: string, job: WorkspaceJob) => Promise<void>): void {
    this.refresh = refresh
  }
  get(id: string): WorkspaceJob | undefined {
    return this.jobs.get(id)
  }
  snapshot(): Record<string, WorkspaceJob> {
    return Object.fromEntries(this.jobs)
  }
}
