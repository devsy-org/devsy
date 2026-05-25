import { tick } from "svelte"
import { render } from "@testing-library/svelte"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import type { Provider } from "$lib/types/index.js"

const providerOptions = vi.fn()
const providerUse = vi.fn()
const providerUpdate = vi.fn()
const providerDelete = vi.fn()
const providerInit = vi.fn()
const providerList = vi.fn()
const providerSetOptions = vi.fn()
const providerRename = vi.fn()

vi.mock("$lib/ipc/commands.js", () => ({
  providerOptions: (...args: unknown[]) => providerOptions(...args),
  providerUse: (...args: unknown[]) => providerUse(...args),
  providerUpdate: (...args: unknown[]) => providerUpdate(...args),
  providerDelete: (...args: unknown[]) => providerDelete(...args),
  providerInit: (...args: unknown[]) => providerInit(...args),
  providerList: (...args: unknown[]) => providerList(...args),
  providerSetOptions: (...args: unknown[]) => providerSetOptions(...args),
  providerRename: (...args: unknown[]) => providerRename(...args),
}))

vi.mock("$lib/stores/providers.js", async () => {
  const { writable } = await import("svelte/store")
  return { providers: writable([]) }
})

vi.mock("$lib/stores/toasts.js", () => ({
  toasts: {
    success: vi.fn(),
    error: vi.fn(),
    info: vi.fn(),
  },
}))

import ProviderSheet from "./ProviderSheet.svelte"

const MOCK_IPC_DELAY_MS = 10
const TIMING_BUDGET_MS = 200

function makeProvider(name: string, extras: Partial<Provider> = {}): Provider {
  return {
    name,
    version: "0.1.0",
    state: { initialized: true },
    ...extras,
  }
}

async function flushAsync() {
  // Allow pending microtasks + setTimeout(10) IPC to resolve, then settle effects.
  await new Promise((r) => setTimeout(r, MOCK_IPC_DELAY_MS + 20))
  await tick()
  await tick()
}

describe("ProviderSheet", () => {
  beforeEach(() => {
    providerOptions.mockReset()
    providerOptions.mockImplementation(async (name: string) => {
      await new Promise((r) => setTimeout(r, MOCK_IPC_DELAY_MS))
      return {
        TOKEN: {
          name: "TOKEN",
          displayName: "Token",
          description: `token for ${name}`,
          required: false,
        },
      }
    })
    providerUse.mockResolvedValue(undefined)
    providerUpdate.mockResolvedValue(undefined)
    providerDelete.mockResolvedValue(undefined)
    providerInit.mockResolvedValue(undefined)
    providerList.mockResolvedValue([])
    providerSetOptions.mockResolvedValue(undefined)
    providerRename.mockResolvedValue(undefined)
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  it("opens and loads options once", async () => {
    const { unmount } = render(ProviderSheet, {
      props: { provider: makeProvider("ssh"), open: true },
    })

    await flushAsync()

    expect(providerOptions).toHaveBeenCalledTimes(1)
    expect(providerOptions).toHaveBeenCalledWith("ssh")
    unmount()
  })

  it("does not refetch when provider prop ref changes but name stays the same", async () => {
    const { rerender, unmount } = render(ProviderSheet, {
      props: { provider: makeProvider("ssh"), open: true },
    })

    await flushAsync()
    expect(providerOptions).toHaveBeenCalledTimes(1)

    // Swap to a brand-new object with the same .name (simulates store poll update).
    await rerender({ provider: makeProvider("ssh"), open: true })
    await flushAsync()
    await rerender({
      provider: makeProvider("ssh", { description: "changed" }),
      open: true,
    })
    await flushAsync()

    expect(providerOptions).toHaveBeenCalledTimes(1)
    unmount()
  })

  it("does refetch when provider.name actually changes", async () => {
    const { rerender, unmount } = render(ProviderSheet, {
      props: { provider: makeProvider("ssh"), open: true },
    })

    await flushAsync()
    expect(providerOptions).toHaveBeenCalledTimes(1)

    await rerender({ provider: makeProvider("docker"), open: true })
    await flushAsync()

    expect(providerOptions).toHaveBeenCalledTimes(2)
    expect(providerOptions).toHaveBeenNthCalledWith(2, "docker")
    unmount()
  })

  it("refetches when toggling open off and back on for same provider", async () => {
    const provider = makeProvider("ssh")
    const { rerender, unmount } = render(ProviderSheet, {
      props: { provider, open: true },
    })

    await flushAsync()
    expect(providerOptions).toHaveBeenCalledTimes(1)

    await rerender({ provider, open: false })
    await flushAsync()

    await rerender({ provider, open: true })
    await flushAsync()

    expect(providerOptions).toHaveBeenCalledTimes(2)
    unmount()
  })

  it("loads within timing budget (benchmark)", async () => {
    const start = performance.now()
    const { unmount } = render(ProviderSheet, {
      props: { provider: makeProvider("ssh"), open: true },
    })

    // Poll until the mock resolves and effect settles.
    while (providerOptions.mock.results.length === 0) {
      await new Promise((r) => setTimeout(r, 1))
    }
    await providerOptions.mock.results[0]!.value
    await tick()
    await tick()

    const elapsed = performance.now() - start
    console.log(
      `ProviderSheet open->loaded elapsed: ${elapsed.toFixed(2)}ms (budget ${TIMING_BUDGET_MS}ms)`,
    )
    expect(elapsed).toBeLessThan(TIMING_BUDGET_MS)
    unmount()
  })
})
