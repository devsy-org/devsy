import { derived, get, writable } from "svelte/store"
import { workspaceSnapshot } from "$lib/ipc/commands.js"
import { onWorkspacesChanged } from "$lib/ipc/events.js"
import type { UnlistenFn } from "$lib/ipc/types.js"
import type {
  Workspace,
  WorkspaceJob,
  WorkspaceStatus,
} from "$lib/types/index.js"
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
  const previous = get(workspaceJobs)
  const pending = Object.entries(jobs).filter(
    ([id, job]) =>
      !updated.some((workspace) => workspace.id === id) &&
      (job.state === "running" ||
        (job.activity === "creating" && job.state !== "succeeded")),
  )
  workspaces.set([...updated, ...pending.map(([id]) => ({ id }))])
  workspaceJobs.set(jobs)
  for (const [id, job] of Object.entries(jobs)) {
    if (job.state === "running") continue
    if (!notify || !previous[id] || notified.has(job.commandId)) {
      notified.add(job.commandId)
      continue
    }
    notified.add(job.commandId)
    if (job.error) toasts.error(`${id}: ${job.error}`)
    else
      toasts.success(
        `${id}: ${job.activity === "deleting" ? "Deleted" : "Operation completed"}`,
      )
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
  unlisten?.()
  unlisten = null
}
