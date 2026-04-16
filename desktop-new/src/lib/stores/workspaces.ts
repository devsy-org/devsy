import type { UnlistenFn } from "@tauri-apps/api/event"
import { writable } from "svelte/store"
import { workspaceList, workspaceStatus } from "$lib/ipc/commands.js"
import { onWorkspacesChanged } from "$lib/ipc/events.js"
import type { Workspace } from "$lib/types/index.js"

export const workspaces = writable<Workspace[]>([])
export const workspacesLoading = writable(true)

let unlisten: UnlistenFn | null = null

export async function initWorkspaces() {
  workspacesLoading.set(true)
  try {
    const list = await workspaceList()
    workspaces.set(list)
    fetchStatuses(list)
  } catch {
    // Tauri not available (e.g. during SSR or browser preview)
  } finally {
    workspacesLoading.set(false)
  }

  try {
    unlisten = await onWorkspacesChanged((updated) => {
      workspaces.set(updated)
      fetchStatuses(updated)
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

/** Fetch status for each workspace and merge into store */
function fetchStatuses(list: Workspace[]) {
  for (const ws of list) {
    workspaceStatus(ws.id)
      .then((raw) => {
        try {
          const parsed = JSON.parse(raw) as { state?: string }
          if (parsed.state) {
            workspaces.update((current) =>
              current.map((w) =>
                w.id === ws.id ? { ...w, status: parsed.state } : w,
              ),
            )
          }
        } catch {
          // Status response wasn't valid JSON — use raw as status
          const status = raw.trim()
          if (status) {
            workspaces.update((current) =>
              current.map((w) => (w.id === ws.id ? { ...w, status } : w)),
            )
          }
        }
      })
      .catch(() => {
        // Status fetch failed — leave as-is
      })
  }
}
