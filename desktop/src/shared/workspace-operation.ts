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

export type WorkspaceStatusTone =
  | "default"
  | "secondary"
  | "outline"
  | "destructive"
  | "warning"

export interface WorkspaceStatusView {
  headline: string
  phase?: string
  tone: WorkspaceStatusTone
  busy: boolean
  announce: string
  recovery?: { message: string; canRetry: boolean }
  error?: string
  detailsAvailable: boolean
}

const HEADLINE: Record<WorkspaceActivity, string> = {
  creating: "Creating",
  starting: "Starting",
  stopping: "Stopping",
  deleting: "Deleting",
  rebuilding: "Rebuilding",
  resetting: "Resetting",
}

const FAILED_HEADLINE: Record<WorkspaceActivity, string> = {
  creating: "Create failed",
  starting: "Start failed",
  stopping: "Stop failed",
  deleting: "Delete failed",
  rebuilding: "Rebuild failed",
  resetting: "Reset failed",
}

const CONFIRMED_TOAST: Record<WorkspaceActivity, string> = {
  creating: "Workspace ready",
  starting: "Workspace running",
  stopping: "Workspace stopped",
  deleting: "Workspace deleted",
  rebuilding: "Workspace rebuilt",
  resetting: "Workspace reset",
}

export function workspaceConfirmedToast(activity: WorkspaceActivity): string {
  return CONFIRMED_TOAST[activity]
}

export function workspaceFailedHeadline(activity: WorkspaceActivity): string {
  return FAILED_HEADLINE[activity]
}

const PHASE_LABELS: Record<string, string> = {
  cloning_repository: "Cloning repository",
  resolving_config: "Resolving configuration",
  initialize_command: "Running initialize command",
  building_image: "Building image",
  starting_container: "Starting container",
  injecting_agent: "Connecting agent",
  running_lifecycle_hook: "Running lifecycle hooks",
  waiting_for: "Waiting",
  running_command: "Running command",
  configuring_workspace: "Configuring workspace",
  configuring_ssh: "Configuring SSH",
  starting_ssh_tunnel: "Starting SSH tunnel",
  launching_ide: "Launching IDE",
  stopping_workspace: "Stopping resources",
  deleting_workspace: "Removing workspace",
  rebuilding_workspace: "Rebuilding workspace",
  resetting_workspace: "Resetting workspace",
  ready: "Ready",
  failed: "Failed",
}

export function humanPhase(raw?: string): string | undefined {
  if (!raw) return undefined
  const mapped = PHASE_LABELS[raw]
  if (mapped) return mapped
  const spaced = raw.replaceAll("_", " ").replaceAll("-", " ").trim()
  if (!spaced) return undefined
  return spaced[0].toUpperCase() + spaced.slice(1)
}

function confirmingPhase(activity: WorkspaceActivity): string {
  return activity === "deleting" ? "Confirming removal" : "Confirming status"
}

function stalledMessage(activity: WorkspaceActivity): string {
  return activity === "deleting"
    ? "List may be out of date"
    : "Status may be out of date"
}

export function presentWorkspaceStatus(input: {
  lifecycle?: string
  job?: WorkspaceJob
}): WorkspaceStatusView {
  const { lifecycle, job } = input
  if (!job || job.state === "succeeded") {
    const headline = lifecycle ?? "Checking"
    return {
      headline,
      tone: lifecycle?.toLowerCase() === "running" ? "default" : "outline",
      busy: false,
      announce: headline,
      detailsAvailable: false,
    }
  }
  if (job.state === "failed") {
    const headline = FAILED_HEADLINE[job.activity]
    return {
      headline,
      tone: "destructive",
      busy: false,
      announce: headline,
      error: job.error ?? "Workspace operation failed",
      detailsAvailable: true,
    }
  }
  const headline = HEADLINE[job.activity]
  if (job.state === "reconciling") {
    if (job.refreshError) {
      const message = stalledMessage(job.activity)
      return {
        headline,
        tone: "warning",
        busy: false,
        announce: `${headline}. ${message}`,
        recovery: { message, canRetry: true },
        detailsAvailable: true,
      }
    }
    if (job.error) {
      const failedHeadline = FAILED_HEADLINE[job.activity]
      return {
        headline: failedHeadline,
        tone: "destructive",
        busy: false,
        announce: failedHeadline,
        error: job.error,
        detailsAvailable: true,
      }
    }
    const phase = confirmingPhase(job.activity)
    return {
      headline,
      phase,
      tone: "secondary",
      busy: true,
      announce: `${headline}. ${phase}`,
      detailsAvailable: true,
    }
  }
  const phase = humanPhase(job.phase)
  return {
    headline,
    phase,
    tone: "secondary",
    busy: true,
    announce: phase ? `${headline}. ${phase}` : headline,
    detailsAvailable: true,
  }
}

export function workspaceJobLabel(job?: WorkspaceJob): string | undefined {
  if (!job || job.state === "succeeded") return undefined
  if (job.state === "failed") return workspaceFailedHeadline(job.activity)
  return job.activity[0].toUpperCase() + job.activity.slice(1)
}
