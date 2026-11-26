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

export interface ToastOptions {
  sticky?: boolean
  action?: { label: string; onClick: () => void }
}

function add(
  message: string,
  variant: Toast["variant"] = "default",
  options?: ToastOptions,
) {
  const id = String(++nextId)
  const duration = options?.sticky ? Infinity : DURATION_MS[variant]
  const toast: Toast = { id, message, variant, timestamp: Date.now(), duration }

  historyStore.update((list) => [toast, ...list].slice(0, MAX_HISTORY))

  const sonnerOptions = {
    duration,
    ...(options?.action ? { action: options.action } : {}),
  }
  if (variant === "success") {
    sonnerToast.success(message, sonnerOptions)
  } else if (variant === "error") {
    sonnerToast.error(message, sonnerOptions)
  } else {
    sonnerToast.info(message, sonnerOptions)
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
  success: (message: string, options?: ToastOptions) =>
    add(message, "success", options),
  error: (message: string, options?: ToastOptions) =>
    add(message, "error", options),
  info: (message: string, options?: ToastOptions) =>
    add(message, "default", options),
  dismiss: (id?: string | number) => sonnerToast.dismiss(id),
}

export const notificationHistory = {
  subscribe: historyStore.subscribe,
  remove: removeFromHistory,
  clear: clearHistory,
  unreadCount,
}
