import { get, writable } from "svelte/store"
import { workspaceList } from "$lib/ipc/commands.js"
import { onWorkspacesChanged } from "$lib/ipc/events.js"
import type { UnlistenFn } from "$lib/ipc/types.js"
import type { Workspace, WorkspaceJob } from "$lib/types/index.js"

export const workspaces = writable<Workspace[]>([])
export const workspacesLoading = writable(true)

// In-flight workspace deletes, keyed by workspace id. Owned by the main
// process so it survives navigation and window reload.
export const workspaceJobs = writable<Record<string, WorkspaceJob>>({})

let unlisten: UnlistenFn | null = null

function mergeWorkspaceStatuses(current: Workspace[], updated: Workspace[]) {
  const statusMap = new Map(current.map((ws) => [ws.id, ws.status]))
  return updated.map((ws) => ({
    ...ws,
    status: ws.status ?? statusMap.get(ws.id),
  }))
}

export async function initWorkspaces() {
  workspacesLoading.set(true)
  try {
    const list = await workspaceList()
    workspaces.set(mergeWorkspaceStatuses(get(workspaces), list))
  } catch {
    // IPC not available (e.g. during browser preview)
  } finally {
    workspacesLoading.set(false)
  }

  try {
    unlisten = await onWorkspacesChanged((updated, jobs) => {
      workspaces.update((current) => mergeWorkspaceStatuses(current, updated))
      workspaceJobs.set(jobs)
    })
  } catch {
    // Event listener setup failed
  }
}

export function destroyWorkspaces() {
  if (unlisten) {
    unlisten()
    unlisten = null
  }
}
