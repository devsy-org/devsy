import { get } from "svelte/store"
import { describe, expect, it, vi } from "vitest"

// Mock svelte-sonner before importing toasts
vi.mock("svelte-sonner", () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
    info: vi.fn(),
    dismiss: vi.fn(),
  },
}))

import { toast as sonnerToast } from "svelte-sonner"
import { notificationHistory, toasts } from "./toasts.js"

describe("toasts store", () => {
  it("success calls sonner.success and adds to history", () => {
    toasts.success("It worked!")
    expect(sonnerToast.success).toHaveBeenCalledWith("It worked!", {
      duration: 5000,
    })
    const history = get(notificationHistory)
    expect(history.length).toBeGreaterThanOrEqual(1)
    expect(history[0].message).toBe("It worked!")
    expect(history[0].variant).toBe("success")
  })

  it("error calls sonner.error and adds to history", () => {
    toasts.error("Something failed")
    expect(sonnerToast.error).toHaveBeenCalledWith("Something failed", {
      duration: 8000,
    })
    const history = get(notificationHistory)
    expect(history[0].message).toBe("Something failed")
    expect(history[0].variant).toBe("error")
  })

  it("info calls sonner.info and adds to history", () => {
    toasts.info("FYI")
    expect(sonnerToast.info).toHaveBeenCalledWith("FYI", { duration: 5000 })
    const history = get(notificationHistory)
    expect(history[0].message).toBe("FYI")
    expect(history[0].variant).toBe("default")
  })

  it("assigns unique ids in history", () => {
    toasts.success("first")
    toasts.error("second")
    const history = get(notificationHistory)
    expect(history[0].id).not.toBe(history[1].id)
  })

  it("dismiss calls sonner.dismiss", () => {
    toasts.dismiss(123)
    expect(sonnerToast.dismiss).toHaveBeenCalledWith(123)
  })

  it("removes an item from history", () => {
    toasts.success("to remove")
    const history = get(notificationHistory)
    const id = history[0].id
    notificationHistory.remove(id)
    const updated = get(notificationHistory)
    expect(updated.find((t) => t.id === id)).toBeUndefined()
  })

  it("clears all history", () => {
    toasts.success("a")
    toasts.error("b")
    notificationHistory.clear()
    expect(get(notificationHistory)).toEqual([])
  })

  it("unreadCount reflects recent items", () => {
    notificationHistory.clear()
    toasts.success("recent")
    expect(get(notificationHistory.unreadCount)).toBe(1)
  })
})
