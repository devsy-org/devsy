import { writable } from "svelte/store"

export interface Toast {
  id: string
  message: string
  variant: "default" | "success" | "error"
}

const { subscribe, update } = writable<Toast[]>([])

let nextId = 0

function add(message: string, variant: Toast["variant"] = "default") {
  const id = String(++nextId)
  update((toasts) => [...toasts, { id, message, variant }])

  setTimeout(() => {
    dismiss(id)
  }, 4000)

  return id
}

function dismiss(id: string) {
  update((toasts) => toasts.filter((t) => t.id !== id))
}

export const toasts = {
  subscribe,
  success: (message: string) => add(message, "success"),
  error: (message: string) => add(message, "error"),
  info: (message: string) => add(message, "default"),
  dismiss,
}
