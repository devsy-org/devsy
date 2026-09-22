import type { TrayNotificationLevel } from "./app-settings.js"
import type { WorkspaceJob } from "../shared/workspace-operation.js"
import type { UpdateStatus } from "./updater.js"

export interface NotificationRequest {
  title: string
  body: string
  onClick: () => void
}

export type NotificationSink = (request: NotificationRequest) => void

export interface TerminalOutcome {
  workspaceId: string
  commandId: string
  activity: WorkspaceJob["activity"]
  state: "succeeded" | "failed"
}

const ACTIVITY_VERBS: Record<WorkspaceJob["activity"], string> = {
  creating: "Create",
  starting: "Start",
  stopping: "Stop",
  deleting: "Delete",
  rebuilding: "Rebuild",
  resetting: "Reset",
}

export function collectTerminalOutcomes(
  previous: Record<string, WorkspaceJob>,
  current: Record<string, WorkspaceJob>,
  alreadyNotified: ReadonlySet<string>,
): TerminalOutcome[] {
  const outcomes: TerminalOutcome[] = []
  for (const [workspaceId, job] of Object.entries(current)) {
    if (job.state !== "succeeded" && job.state !== "failed") continue
    if (alreadyNotified.has(job.commandId)) continue
    const before = previous[workspaceId]
    if (
      before?.commandId === job.commandId &&
      (before.state === "succeeded" || before.state === "failed")
    )
      continue
    outcomes.push({
      workspaceId,
      commandId: job.commandId,
      activity: job.activity,
      state: job.state,
    })
  }
  return outcomes
}

export function outcomeNotifies(
  outcome: TerminalOutcome,
  level: TrayNotificationLevel,
): boolean {
  if (level === "off") return false
  if (level === "failures") return outcome.state === "failed"
  return true
}

export function outcomeNotificationBody(outcome: TerminalOutcome): string {
  const verb = ACTIVITY_VERBS[outcome.activity]
  return outcome.state === "succeeded"
    ? `${outcome.workspaceId}: ${verb} completed`
    : `${outcome.workspaceId}: ${verb} failed`
}

export function updateNotifies(
  status: UpdateStatus,
  level: TrayNotificationLevel,
  lastNotifiedVersion: string | undefined,
): { notifies: boolean; version?: string; key?: string; title: string; body: string } {
  const none = { notifies: false, title: "", body: "" }
  if (level === "off") return none
  if (!("availableVersion" in status)) return none
  const version = status.availableVersion
  if (!version) return none
  if (status.state === "downloaded") {
    if (level !== "all") return none
    const key = `downloaded:${version}`
    if (lastNotifiedVersion === version || lastNotifiedVersion === key) return none
    return {
      notifies: true,
      version,
      key,
      title: "Devsy update ready",
      body: `Version ${version} is ready to install.`,
    }
  }
  if (status.state === "error" && status.code === "install-failed") {
    const key = `install-failed:${version}`
    if (lastNotifiedVersion === key) return none
    return {
      notifies: true,
      version,
      key,
      title: "Devsy update failed",
      body: `Version ${version} could not be installed.`,
    }
  }
  return none
}

export interface TrayNotifierDeps {
  getLevel: () => TrayNotificationLevel
  isAppFocused: () => boolean
  sink: NotificationSink
  openWorkspace: (workspaceId: string) => void
  openWorkspaceLogs: (workspaceId: string) => void
  openUpdates: () => void
}

const NOTIFIED_CAP = 200

export class TrayNotifier {
  private previous: Record<string, WorkspaceJob> | null = null
  private notified = new Set<string>()
  private lastUpdateNotification: string | undefined

  constructor(private deps: TrayNotifierDeps) {}

  onJobsChanged(current: Record<string, WorkspaceJob>): void {
    // The first snapshot only seeds state: jobs recovered or discovered at
    // launch show current state and never notify.
    if (this.previous === null) {
      this.previous = current
      for (const job of Object.values(current)) {
        if (job.state === "succeeded" || job.state === "failed")
          this.markNotified(job.commandId)
      }
      return
    }
    const outcomes = collectTerminalOutcomes(this.previous, current, this.notified)
    this.previous = current
    for (const outcome of outcomes) {
      this.markNotified(outcome.commandId)
      if (!outcomeNotifies(outcome, this.deps.getLevel())) continue
      if (this.deps.isAppFocused()) continue
      this.deps.sink({
        title: "Devsy",
        body: outcomeNotificationBody(outcome),
        onClick: () =>
          outcome.state === "failed"
            ? this.deps.openWorkspaceLogs(outcome.workspaceId)
            : this.deps.openWorkspace(outcome.workspaceId),
      })
    }
  }

  onUpdateStatus(status: UpdateStatus): void {
    const decision = updateNotifies(
      status,
      this.deps.getLevel(),
      this.lastUpdateNotification,
    )
    if (!decision.notifies || !decision.version) return
    this.lastUpdateNotification = decision.key ?? decision.version
    if (this.deps.isAppFocused()) return
    this.deps.sink({
      title: decision.title,
      body: decision.body,
      onClick: () => this.deps.openUpdates(),
    })
  }

  private markNotified(commandId: string): void {
    this.notified.add(commandId)
    if (this.notified.size > NOTIFIED_CAP) {
      const oldest = this.notified.values().next().value
      if (oldest !== undefined) this.notified.delete(oldest)
    }
  }
}
