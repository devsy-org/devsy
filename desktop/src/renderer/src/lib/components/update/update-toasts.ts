import { toast } from "svelte-sonner"
import { installUpdate } from "$lib/ipc/commands.js"
import type { UpdateStatus } from "$lib/ipc/events.js"
import { subscribe } from "$lib/stores/updates.svelte.js"

let openDialog: (() => void) | null = null
let lastKey = ""
let userInitiated = false

function dedupeKey(s: UpdateStatus): string {
  const version =
    s.state === "available" || s.state === "downloading" || s.state === "downloaded"
      ? s.availableVersion
      : s.currentVersion
  const code = "code" in s ? (s.code ?? "") : ""
  const error = "error" in s ? (s.error ?? "") : ""
  return [s.state, version, code, error].join("")
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

function fireAvailable(
  s: Extract<UpdateStatus, { state: "available" }>,
  autoDownload: boolean,
): void {
  userInitiated = false
  const version = s.availableVersion
  if (autoDownload) {
    toast.info(`Update v${version} found, downloading…`, { duration: 4000 })
    return
  }
  toast(`Update v${version} available`, {
    action: { label: "View", onClick: () => openDialog?.() },
    duration: 10000,
  })
}

function fireDownloaded(s: Extract<UpdateStatus, { state: "downloaded" }>): void {
  const version = s.availableVersion
  toast.success(`Update v${version} ready`, {
    duration: Infinity,
    action: {
      label: "Restart",
      onClick: () => {
        installUpdate().catch(() => {
          toast.error("Failed to start update. Try restarting the app manually.")
        })
      },
    },
  })
}

function fireError(s: Extract<UpdateStatus, { state: "error" }>): void {
  if (!userInitiated) return
  if (s.code === "dev-mode") return
  toast.error(`Update check failed: ${s.error ?? "unknown error"}`, {
    action: { label: "Retry", onClick: () => openDialog?.() },
  })
  userInitiated = false
}

function fireNotAvailable(
  s: Extract<UpdateStatus, { state: "up-to-date" | "not-available" }>,
): void {
  if (!userInitiated) return
  if (s.code === "channel-missing") {
    toast.info("No releases are available on this channel yet.")
  } else {
    toast.success("Devsy is up to date.")
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
    else if (s.state === "up-to-date" || s.state === "not-available") fireNotAvailable(s)
  })
}
