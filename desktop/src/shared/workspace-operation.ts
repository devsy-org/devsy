import type { OperationStatus } from "./cli-error.js"

export type WorkspaceActivity =
  | "creating"
  | "starting"
  | "stopping"
  | "deleting"
  | "rebuilding"
  | "resetting"
export interface WorkspaceJob {
  commandId: string
  activity: WorkspaceActivity
  state: "running" | "reconciling" | "succeeded" | "failed"
  phase: string
  status?: OperationStatus
  error?: string
  refreshError?: string
}

export function workspaceJobBusy(job?: WorkspaceJob): boolean {
  return (
    job?.state === "running" || (job?.state === "reconciling" && !job.error)
  )
}

export function workspaceJobInterruptible(job?: WorkspaceJob): boolean {
  return (
    job?.state === "running" &&
    (job.activity === "starting" || job.activity === "creating")
  )
}

export function workspaceJobLabel(job?: WorkspaceJob): string | undefined {
  if (!job || job.state === "succeeded") return undefined
  const action = job.activity[0].toUpperCase() + job.activity.slice(1)
  if (job.error)
    return `${{ creating: "Create", starting: "Start", stopping: "Stop", deleting: "Delete", rebuilding: "Rebuild", resetting: "Reset" }[job.activity]} failed`
  if (job.state === "reconciling" && job.activity === "deleting" && !job.error)
    return "Deleted"
  return action
}

export function workspaceJobPhase(job?: WorkspaceJob): string | undefined {
  if (!job || job.state === "succeeded") return undefined
  if (job.refreshError)
    return job.activity === "deleting"
      ? "Unable to refresh list"
      : "Unable to refresh status"
  return job.phase
}
