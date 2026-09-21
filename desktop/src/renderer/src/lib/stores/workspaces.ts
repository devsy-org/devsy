import { derived, writable } from "svelte/store"
import { workspaceSnapshot } from "$lib/ipc/commands.js"
import { onWorkspacesChanged } from "$lib/ipc/events.js"
import type { UnlistenFn } from "$lib/ipc/types.js"
import { goto } from "$lib/router.js"
import type {
  Workspace,
  WorkspaceJob,
  WorkspaceStatus,
} from "$lib/types/index.js"
import {
  workspaceConfirmedToast,
  workspaceFailedHeadline,
} from "$shared/workspace-operation.js"
import { toasts } from "./toasts.js"

export const workspaces = writable<Workspace[]>([])
export const workspacesLoading = writable(true)
export const workspaceJobs = writable<Record<string, WorkspaceJob>>({})
export const workspaceStatuses = derived(workspaceJobs, ($jobs) => {
  const statuses: Record<string, WorkspaceStatus> = {}
  for (const [id, job] of Object.entries($jobs)) {
    if (job.status)
      statuses[id] = {
        ...job.status,
        workspaceId: id,
        commandId: job.commandId,
      }
  }
  return statuses
})

let unlisten: UnlistenFn | null = null
let lifecycle = 0
let revision = -1
const notified = new Set<string>()

function apply(
  updated: Workspace[],
  jobs: Record<string, WorkspaceJob>,
  nextRevision: number,
  notify: boolean,
) {
  if (nextRevision <= revision) return
  revision = nextRevision
  const pending = Object.entries(jobs).filter(
    ([id, job]) =>
      !updated.some((workspace) => workspace.id === id) &&
      (job.state === "running" ||
        (job.activity === "creating" && job.state !== "succeeded")),
  )
  workspaces.set([...updated, ...pending.map(([id]) => ({ id }))])
  workspaceJobs.set(jobs)
  for (const [id, job] of Object.entries(jobs)) {
    if (job.state === "running" || job.state === "reconciling") continue
    if (!notify || notified.has(job.commandId)) {
      notified.add(job.commandId)
      continue
    }
    notified.add(job.commandId)
    if (job.state === "failed")
      toasts.error(
        `${id}: ${workspaceFailedHeadline(job.activity)} - ${job.error ?? "Workspace operation failed"}`,
        {
          sticky: true,
          action: {
            label: "View logs",
            onClick: () => goto(`/workspaces/${id}?tab=logs`),
          },
        },
      )
    else toasts.success(`${id}: ${workspaceConfirmedToast(job.activity)}`)
  }
}

export async function initWorkspaces() {
  destroyWorkspaces()
  const epoch = lifecycle
  revision = -1
  workspacesLoading.set(true)
  try {
    const stop = await onWorkspacesChanged((updated, jobs, nextRevision) => {
      if (epoch === lifecycle) apply(updated, jobs, nextRevision, true)
    })
    if (epoch !== lifecycle) {
      stop()
      return
    }
    unlisten = stop
    const snapshot = await workspaceSnapshot()
    if (epoch === lifecycle)
      apply(snapshot.workspaces, snapshot.jobs, snapshot.revision, false)
  } catch {
    // Main process may be unavailable during shutdown/browser preview.
  } finally {
    if (epoch === lifecycle) workspacesLoading.set(false)
  }
}

export function destroyWorkspaces() {
  lifecycle++
  notified.clear()
  unlisten?.()
  unlisten = null
}
