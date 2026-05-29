import { describe, it, expect, vi, beforeEach } from "vitest"

const toastFns = {
  info: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
  default: vi.fn(),
}

vi.mock("svelte-sonner", () => {
  const fn = Object.assign(
    (...args: unknown[]) => toastFns.default(...args),
    toastFns,
  )
  return { toast: fn }
})

vi.mock("$lib/ipc/commands.js", () => ({
  installUpdate: vi.fn(),
}))

let listeners: Array<(s: unknown) => void> = []
vi.mock("$lib/stores/updates.svelte.js", () => ({
  subscribe(fn: (s: unknown) => void) {
    listeners.push(fn)
    return () => {
      const i = listeners.indexOf(fn)
      if (i >= 0) listeners.splice(i, 1)
    }
  },
}))

describe("update-toasts", () => {
  beforeEach(() => {
    listeners = []
    toastFns.info.mockClear()
    toastFns.success.mockClear()
    toastFns.error.mockClear()
    toastFns.default.mockClear()
    vi.resetModules()
  })

  it("fires the available toast once per version even with repeat events", async () => {
    const { initUpdateToasts } = await import("./update-toasts.js")
    initUpdateToasts(() => true)
    expect(listeners.length).toBe(1)
    const emit = listeners[0]

    emit({ state: "available", version: "1.0.0" })
    emit({ state: "available", version: "1.0.0" })
    emit({ state: "available", version: "1.0.0" })

    expect(toastFns.info).toHaveBeenCalledTimes(1)
  })

  it("re-fires when version changes", async () => {
    const { initUpdateToasts } = await import("./update-toasts.js")
    initUpdateToasts(() => true)
    const emit = listeners[0]

    emit({ state: "available", version: "1.0.0" })
    emit({ state: "available", version: "1.0.1" })

    expect(toastFns.info).toHaveBeenCalledTimes(2)
  })

  it("stays silent on background error (no markUserInitiated)", async () => {
    const { initUpdateToasts } = await import("./update-toasts.js")
    initUpdateToasts(() => true)
    const emit = listeners[0]

    emit({ state: "error", error: "feed down", code: "feed-error" })

    expect(toastFns.error).not.toHaveBeenCalled()
  })

  it("fires error toast after markUserInitiated, then resets the flag", async () => {
    const { initUpdateToasts, markUserInitiated } = await import("./update-toasts.js")
    initUpdateToasts(() => true)
    const emit = listeners[0]

    markUserInitiated()
    emit({ state: "error", error: "feed down", code: "feed-error" })
    expect(toastFns.error).toHaveBeenCalledTimes(1)

    // Different error to bypass dedupe; flag should already be reset, so silent.
    emit({ state: "error", error: "different", code: "network" })
    expect(toastFns.error).toHaveBeenCalledTimes(1)
  })

  it("suppresses error toast when code is dev-mode", async () => {
    const { initUpdateToasts, markUserInitiated } = await import("./update-toasts.js")
    initUpdateToasts(() => true)
    const emit = listeners[0]

    markUserInitiated()
    emit({ state: "error", error: "x", code: "dev-mode" })

    expect(toastFns.error).not.toHaveBeenCalled()
  })

  it("openUpdateDialog calls the bound opener", async () => {
    const { bindDialogOpener, openUpdateDialog } = await import("./update-toasts.js")
    const opener = vi.fn()
    bindDialogOpener(opener)
    openUpdateDialog()
    expect(opener).toHaveBeenCalledTimes(1)
  })

  it("fires action toast (not info) when auto-download is off", async () => {
    const { initUpdateToasts } = await import("./update-toasts.js")
    initUpdateToasts(() => false)
    const emit = listeners[0]

    emit({ state: "available", version: "1.0.0" })

    expect(toastFns.info).not.toHaveBeenCalled()
    expect(toastFns.default).toHaveBeenCalledTimes(1)
  })

  it("re-fires available after a downloading transition for a new version", async () => {
    const { initUpdateToasts } = await import("./update-toasts.js")
    initUpdateToasts(() => true)
    const emit = listeners[0]

    emit({ state: "available", version: "1.0.0" })
    emit({ state: "downloading", version: "1.0.0", progress: { percent: 50, bytesPerSecond: 0, transferred: 0, total: 0 } })
    emit({ state: "available", version: "2.0.0" })

    expect(toastFns.info).toHaveBeenCalledTimes(2)
  })
})
