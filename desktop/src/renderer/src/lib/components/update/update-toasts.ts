import { toast } from "svelte-sonner"
import { installUpdate } from "$lib/ipc/commands.js"
import type { UpdateStatus } from "$lib/ipc/events.js"
import { subscribe } from "$lib/stores/updates.svelte.js"

let openDialog: (() => void) | null = null
let lastKey = ""
let userInitiated = false

function dedupeKey(s: UpdateStatus): string {
  // Include error text and code so consecutive errors (or a retry after
  // an error) are not suppressed.
  return [s.state, s.version ?? "", s.code ?? "", s.error ?? ""].join("")
}

export function bindDialogOpener(fn: () => void): void {
  openDialog = fn
}

export function openUpdateDialog(): void {
  openDialog?.()
}

export function markUserInitiated(): void {
  userInitiated = true
}

function fireAvailable(s: UpdateStatus, autoDownload: boolean): void {
  if (autoDownload) {
    toast.info(`Update v${s.version} found, downloading…`, { duration: 4000 })
    return
  }
  toast(`Update v${s.version} available`, {
    action: { label: "View", onClick: () => openDialog?.() },
    duration: 10000,
  })
}

function fireDownloaded(s: UpdateStatus): void {
  toast.success(`Update v${s.version} ready`, {
    duration: Infinity,
    action: {
      label: "Restart and Update",
      onClick: () => {
        installUpdate().catch(() => {
          toast.error("Failed to start update. Try restarting the app manually.")
        })
      },
    },
  })
}

function fireError(s: UpdateStatus): void {
  if (!userInitiated) return
  if (s.code === "dev-mode") return
  toast.error(`Update check failed: ${s.error ?? "unknown error"}`, {
    action: { label: "Retry", onClick: () => openDialog?.() },
  })
  userInitiated = false
}

function fireNotAvailable(s: UpdateStatus): void {
  if (!userInitiated) return
  if (s.code === "channel-missing") {
    toast.info("No releases are available on this channel yet.")
  } else {
    toast.success("You're on the latest version.")
  }
  userInitiated = false
}

export function initUpdateToasts(getAutoDownload: () => boolean): () => void {
  return subscribe((s) => {
    const key = dedupeKey(s)
    if (key === lastKey) return
    lastKey = key

    if (s.state === "available") fireAvailable(s, getAutoDownload())
    else if (s.state === "downloaded") fireDownloaded(s)
    else if (s.state === "error") fireError(s)
    else if (s.state === "not-available") fireNotAvailable(s)
  })
}
