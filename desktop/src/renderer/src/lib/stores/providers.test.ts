import { get } from "svelte/store"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import {
  mockInvoke,
  mockListen,
  resetTauriMocks,
} from "$lib/__mocks__/tauri.js"

import {
  destroyProviders,
  initProviders,
  providers,
  providersLoading,
} from "./providers.js"

describe("providers store", () => {
  beforeEach(() => {
    resetTauriMocks()
    providers.set([])
    providersLoading.set(true)
  })

  afterEach(() => {
    destroyProviders()
  })

  it("loads providers on init", async () => {
    const mockProviders = [
      { name: "docker", version: "0.5.0" },
      { name: "aws", version: "0.3.0" },
    ]
    mockInvoke.mockResolvedValue(mockProviders)

    await initProviders()

    expect(get(providersLoading)).toBe(false)
    const current = get(providers)
    expect(current).toHaveLength(2)
    expect(current[0].name).toBe("docker")
    expect(current[1].name).toBe("aws")
  })

  it("sets loading false even on error", async () => {
    mockInvoke.mockRejectedValue(new Error("Tauri not available"))

    await initProviders()

    expect(get(providersLoading)).toBe(false)
    expect(get(providers)).toEqual([])
  })

  it("subscribes to provider change events", async () => {
    mockInvoke.mockResolvedValue([])

    await initProviders()

    expect(mockListen).toHaveBeenCalledWith(
      "providers-changed",
      expect.any(Function),
    )
  })

  it("updates providers when event fires", async () => {
    mockInvoke.mockResolvedValue([])
    let eventCallback:
      | ((event: { payload: { providers: unknown[] } }) => void)
      | undefined
    mockListen.mockImplementation(
      (
        _name: string,
        cb: (event: { payload: { providers: unknown[] } }) => void,
      ) => {
        eventCallback = cb
        return Promise.resolve(() => {})
      },
    )

    await initProviders()
    expect(get(providers)).toEqual([])

    eventCallback?.({
      payload: { providers: [{ name: "kubernetes", version: "1.0" }] },
    })
    expect(get(providers)).toHaveLength(1)
    expect(get(providers)[0].name).toBe("kubernetes")
  })

  it("destroyProviders cleans up listener", async () => {
    const mockUnlisten = vi.fn()
    mockListen.mockResolvedValue(mockUnlisten)
    mockInvoke.mockResolvedValue([])

    await initProviders()
    destroyProviders()

    expect(mockUnlisten).toHaveBeenCalled()
  })

  it("destroyProviders is safe to call without init", () => {
    expect(() => destroyProviders()).not.toThrow()
  })
})
