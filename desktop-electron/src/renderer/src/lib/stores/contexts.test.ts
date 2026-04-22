import { get } from "svelte/store"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import {
  mockInvoke,
  mockListen,
  resetTauriMocks,
} from "$lib/__mocks__/tauri.js"

import {
  activeContext,
  contexts,
  contextsLoading,
  destroyContexts,
  initContexts,
} from "./contexts.js"

describe("contexts store", () => {
  beforeEach(() => {
    resetTauriMocks()
    contexts.set([])
    activeContext.set("")
    contextsLoading.set(true)
  })

  afterEach(() => {
    destroyContexts()
  })

  it("loads contexts and active context on init", async () => {
    mockInvoke.mockResolvedValue({
      contexts: [
        { name: "default", options: {} },
        { name: "production", options: {} },
      ],
      activeContext: "default",
    })

    await initContexts()

    expect(get(contextsLoading)).toBe(false)
    expect(get(contexts)).toHaveLength(2)
    expect(get(contexts)[0].name).toBe("default")
    expect(get(activeContext)).toBe("default")
  })

  it("sets loading false even on error", async () => {
    mockInvoke.mockRejectedValue(new Error("Tauri not available"))

    await initContexts()

    expect(get(contextsLoading)).toBe(false)
    expect(get(contexts)).toEqual([])
    expect(get(activeContext)).toBe("")
  })

  it("subscribes to context change events", async () => {
    mockInvoke.mockResolvedValue({ contexts: [], activeContext: "" })

    await initContexts()

    expect(mockListen).toHaveBeenCalledWith(
      "contexts-changed",
      expect.any(Function),
    )
  })

  it("updates contexts and active context when event fires", async () => {
    mockInvoke.mockResolvedValue({ contexts: [], activeContext: "" })
    let eventCallback:
      | ((event: {
          payload: { contexts: unknown[]; activeContext: string }
        }) => void)
      | undefined
    mockListen.mockImplementation(
      (
        _name: string,
        cb: (event: {
          payload: { contexts: unknown[]; activeContext: string }
        }) => void,
      ) => {
        eventCallback = cb
        return Promise.resolve(() => {})
      },
    )

    await initContexts()

    eventCallback?.({
      payload: {
        contexts: [{ name: "staging", options: {} }],
        activeContext: "staging",
      },
    })

    expect(get(contexts)).toHaveLength(1)
    expect(get(contexts)[0].name).toBe("staging")
    expect(get(activeContext)).toBe("staging")
  })

  it("destroyContexts cleans up listener", async () => {
    const mockUnlisten = vi.fn()
    mockListen.mockResolvedValue(mockUnlisten)
    mockInvoke.mockResolvedValue({ contexts: [], activeContext: "" })

    await initContexts()
    destroyContexts()

    expect(mockUnlisten).toHaveBeenCalled()
  })

  it("destroyContexts is safe to call without init", () => {
    expect(() => destroyContexts()).not.toThrow()
  })
})
