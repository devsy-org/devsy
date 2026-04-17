import { derived, writable } from "svelte/store"
import { toast as sonnerToast } from "svelte-sonner"

export interface Toast {
  id: string
  message: string
  variant: "default" | "success" | "error"
  timestamp: number
  duration: number
}

export const DURATION_MS: Record<Toast["variant"], number> = {
  error: 8000,
  success: 5000,
  default: 5000,
}

const MAX_HISTORY = 50

const historyStore = writable<Toast[]>([])

let nextId = 0

function add(message: string, variant: Toast["variant"] = "default") {
  const id = String(++nextId)
  const duration = DURATION_MS[variant]
  const toast: Toast = { id, message, variant, timestamp: Date.now(), duration }

  historyStore.update((list) => [toast, ...list].slice(0, MAX_HISTORY))

  if (variant === "success") {
    sonnerToast.success(message, { duration })
  } else if (variant === "error") {
    sonnerToast.error(message, { duration })
  } else {
    sonnerToast.info(message, { duration })
  }

  return id
}

function removeFromHistory(id: string) {
  historyStore.update((list) => list.filter((t) => t.id !== id))
}

function clearHistory() {
  historyStore.set([])
}

const unreadCount = derived(historyStore, ($history) => {
  const fiveMinAgo = Date.now() - 5 * 60 * 1000
  return $history.filter((t) => t.timestamp > fiveMinAgo).length
})

export const toasts = {
  success: (message: string) => add(message, "success"),
  error: (message: string) => add(message, "error"),
  info: (message: string) => add(message, "default"),
  dismiss: (id?: string | number) => sonnerToast.dismiss(id),
}

export const notificationHistory = {
  subscribe: historyStore.subscribe,
  remove: removeFromHistory,
  clear: clearHistory,
  unreadCount,
}
