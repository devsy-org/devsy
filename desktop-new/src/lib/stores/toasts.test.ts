import { get } from "svelte/store"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { toasts } from "./toasts.js"

describe("toasts store", () => {
  beforeEach(() => {
    vi.useFakeTimers()
    // Clear any existing toasts
    const current = get(toasts)
    for (const t of current) {
      toasts.dismiss(t.id)
    }
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it("starts empty", () => {
    expect(get(toasts)).toEqual([])
  })

  it("adds a success toast", () => {
    toasts.success("It worked!")
    const current = get(toasts)
    expect(current).toHaveLength(1)
    expect(current[0].message).toBe("It worked!")
    expect(current[0].variant).toBe("success")
  })

  it("adds an error toast", () => {
    toasts.error("Something failed")
    const current = get(toasts)
    expect(current).toHaveLength(1)
    expect(current[0].variant).toBe("error")
  })

  it("adds an info toast with default variant", () => {
    toasts.info("FYI")
    const current = get(toasts)
    expect(current).toHaveLength(1)
    expect(current[0].variant).toBe("default")
  })

  it("assigns unique ids to each toast", () => {
    toasts.success("first")
    toasts.error("second")
    const current = get(toasts)
    expect(current).toHaveLength(2)
    expect(current[0].id).not.toBe(current[1].id)
  })

  it("dismisses a toast by id", () => {
    const id = toasts.success("will be dismissed")
    expect(get(toasts)).toHaveLength(1)
    toasts.dismiss(id)
    expect(get(toasts)).toHaveLength(0)
  })

  it("auto-dismisses after 4 seconds", () => {
    toasts.success("temporary")
    expect(get(toasts)).toHaveLength(1)

    vi.advanceTimersByTime(3999)
    expect(get(toasts)).toHaveLength(1)

    vi.advanceTimersByTime(1)
    expect(get(toasts)).toHaveLength(0)
  })

  it("dismissing non-existent id is a no-op", () => {
    toasts.success("stays")
    toasts.dismiss("nonexistent-id")
    expect(get(toasts)).toHaveLength(1)
  })

  it("handles multiple toasts with independent auto-dismiss", () => {
    toasts.success("first")
    vi.advanceTimersByTime(2000)
    toasts.error("second")

    expect(get(toasts)).toHaveLength(2)

    // First toast should dismiss at t=4000
    vi.advanceTimersByTime(2000)
    expect(get(toasts)).toHaveLength(1)
    expect(get(toasts)[0].message).toBe("second")

    // Second toast should dismiss at t=6000
    vi.advanceTimersByTime(2000)
    expect(get(toasts)).toHaveLength(0)
  })
})
