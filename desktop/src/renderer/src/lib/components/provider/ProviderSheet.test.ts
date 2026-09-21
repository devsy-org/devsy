import { render } from "@testing-library/svelte"
import { tick } from "svelte"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import type { CommandProgress, Provider, ProviderJob } from "$lib/types/index.js"

const providerOptions = vi.fn()
const providerUse = vi.fn()
const providerUpdateStreaming = vi.fn()
const providerDelete = vi.fn()
const providerInit = vi.fn()
const providerList = vi.fn()
const providerRefreshState = vi.fn()
const providerSetOptions = vi.fn()
const providerRename = vi.fn()
const providerSetVersion = vi.fn()
const loadVersionsFor = vi.fn()
const refreshUpdates = vi.fn()
const providerJobsBox = vi.hoisted(() => ({
  store: undefined as unknown as import("svelte/store").Writable<Record<string, ProviderJob>>,
}))
let progressCallback: ((progress: CommandProgress) => void) | null = null

vi.mock("$lib/ipc/commands.js", () => ({
  providerOptions: (...args: unknown[]) => providerOptions(...args),
  providerUse: (...args: unknown[]) => providerUse(...args),
  providerUpdateStreaming: (...args: unknown[]) => providerUpdateStreaming(...args),
  providerDelete: (...args: unknown[]) => providerDelete(...args),
  providerInit: (...args: unknown[]) => providerInit(...args),
  providerList: (...args: unknown[]) => providerList(...args),
  providerRefreshState: (...args: unknown[]) => providerRefreshState(...args),
  providerSetOptions: (...args: unknown[]) => providerSetOptions(...args),
  providerRename: (...args: unknown[]) => providerRename(...args),
  providerSetVersion: (...args: unknown[]) => providerSetVersion(...args),
}))

vi.mock("$lib/ipc/events.js", () => ({
  onCommandProgress: vi.fn(async (callback: (progress: CommandProgress) => void) => {
    progressCallback = callback
    return () => { progressCallback = null }
  }),
}))

vi.mock("$lib/stores/providers.js", async () => {
  const { writable } = await import("svelte/store")
  providerJobsBox.store = writable({})
  return {
    providers: writable([]),
    providerJobs: providerJobsBox.store,
  }
})

vi.mock("$lib/stores/providerVersions.js", async () => {
  const { writable } = await import("svelte/store")
  return {
    providerVersions: writable({
      byProvider: {
        ssh: {
          versions: [
            { tag: "0.1.0", current: true },
            { tag: "0.2.0", current: false },
          ],
          unsupported: false,
        },
      },
      updates: {
        ssh: { updateAvailable: true, current: "0.1.0", latest: "0.2.0" },
      },
      lastCheckedAt: null,
    }),
    loadVersionsFor: (...args: unknown[]) => loadVersionsFor(...args),
    refreshUpdates: (...args: unknown[]) => refreshUpdates(...args),
  }
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
    progressCallback = null
    providerJobsBox.store.set({})
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
    providerUpdateStreaming.mockResolvedValue("update-command-1")
    providerDelete.mockResolvedValue(undefined)
    providerInit.mockResolvedValue(undefined)
    providerList.mockResolvedValue([])
    providerRefreshState.mockResolvedValue(undefined)
    providerSetOptions.mockResolvedValue(undefined)
    providerRename.mockResolvedValue(undefined)
    providerSetVersion.mockResolvedValue(undefined)
    loadVersionsFor.mockResolvedValue(undefined)
    refreshUpdates.mockResolvedValue(undefined)
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

  it("hides 'Set Default' button when provider is default", async () => {
    const { unmount } = render(ProviderSheet, {
      props: { provider: makeProvider("ssh", { isDefault: true }), open: true },
    })

    await flushAsync()

    // Find the "Set Default" button by exact text match
    const buttons = Array.from(document.querySelectorAll("button"))
    const setDefaultButton = buttons.find((btn) => btn.textContent?.trim() === "Set Default")
    expect(setDefaultButton).toBeUndefined()
    unmount()
  })

  it("shows 'Set Default' button when provider is not default", async () => {
    const { unmount } = render(ProviderSheet, {
      props: { provider: makeProvider("ssh"), open: true },
    })

    await flushAsync()

    // Find the "Set Default" button by exact text match
    const buttons = Array.from(document.querySelectorAll("button"))
    const setDefaultButton = buttons.find((btn) => btn.textContent?.trim() === "Set Default")
    expect(setDefaultButton).toBeDefined()
    unmount()
  })

  it("renders the version selector when versions are known", async () => {
    const { unmount } = render(ProviderSheet, {
      props: { provider: makeProvider("ssh"), open: true },
    })

    await flushAsync()

    // Select trigger contains the current tag.
    const triggers = Array.from(document.querySelectorAll("button"))
    const versionTrigger = triggers.find((b) => b.textContent?.includes("0.1.0"))
    expect(versionTrigger).toBeDefined()
    unmount()
  })

  it("clicking Update opens the update confirm dialog", async () => {
    const { unmount } = render(ProviderSheet, {
      props: { provider: makeProvider("ssh"), open: true },
    })

    await flushAsync()

    const updateBtn = Array.from(document.querySelectorAll("button")).find(
      (b) => b.textContent?.trim() === "Update",
    )
    updateBtn?.click()
    await tick()

    // Confirm dialog title mentions the latest tag from the mocked store.
    const dialogs = Array.from(document.querySelectorAll("[role='dialog']"))
    const confirmDialog = dialogs.find((d) =>
      d.textContent?.includes("Update 'ssh' to 0.2.0"),
    )
    expect(confirmDialog).toBeDefined()
    expect(providerUpdateStreaming).not.toHaveBeenCalled()
    unmount()
  })

  it("confirming the update dialog starts one streaming provider update", async () => {
    const { unmount } = render(ProviderSheet, {
      props: { provider: makeProvider("ssh"), open: true },
    })

    await flushAsync()

    const updateBtn = Array.from(document.querySelectorAll("button")).find(
      (b) => b.textContent?.trim() === "Update",
    )
    updateBtn?.click()
    await tick()

    const dialogs2 = Array.from(document.querySelectorAll("[role='dialog']"))
    const confirmDialog = dialogs2.find((d) =>
      d.textContent?.includes("Update 'ssh' to 0.2.0"),
    )
    const confirmBtn = Array.from(
      confirmDialog?.querySelectorAll("button") ?? [],
    ).find((b) => b.textContent?.trim() === "Update")
    confirmBtn?.click()
    await flushAsync()

    expect(providerUpdateStreaming).toHaveBeenCalledTimes(1)
    expect(providerUpdateStreaming).toHaveBeenCalledWith("ssh")
    unmount()
  })

  it("keeps a failed update in the sheet and retries it", async () => {
    const { unmount } = render(ProviderSheet, {
      props: { provider: makeProvider("ssh"), open: true },
    })
    await flushAsync()

    const updateBtn = Array.from(document.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Update",
    )
    updateBtn?.click()
    await tick()
    const confirmDialog = Array.from(document.querySelectorAll("[role='dialog']")).find(
      (dialog) => dialog.textContent?.includes("Update 'ssh' to 0.2.0"),
    )
    Array.from(confirmDialog?.querySelectorAll("button") ?? [])
      .find((button) => button.textContent?.trim() === "Update")
      ?.click()
    await flushAsync()

    progressCallback?.({
      commandId: "update-command-1",
      message: "download timed out",
      success: false,
      done: true,
      cliError: { code: "NETWORK", message: "The download timed out." },
    })
    await tick()

    expect(document.body.textContent).toContain("Update failed")
    expect(document.body.textContent).toContain("The download timed out.")
    const retry = Array.from(document.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Retry update",
    )
    retry?.click()
    await tick()
    expect(providerUpdateStreaming).toHaveBeenCalledTimes(2)
    unmount()
  })


  it("captures terminal progress emitted before a retry returns", async () => {
    providerUpdateStreaming
      .mockResolvedValueOnce("update-command-1")
      .mockImplementationOnce(async () => {
        progressCallback?.({
          commandId: "update-command-2",
          success: true,
          done: true,
        })
        return "update-command-2"
      })
    const { unmount } = render(ProviderSheet, {
      props: { provider: makeProvider("ssh"), open: true },
    })
    await flushAsync()
    const updateBtn = Array.from(document.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Update",
    )
    updateBtn?.click()
    await tick()
    const dialog = Array.from(document.querySelectorAll("[role='dialog']")).find(
      (node) => node.textContent?.includes("Update 'ssh' to 0.2.0"),
    )
    Array.from(dialog?.querySelectorAll("button") ?? [])
      .find((button) => button.textContent?.trim() === "Update")?.click()
    await flushAsync()
    progressCallback?.({
      commandId: "update-command-1",
      success: false,
      done: true,
      cliError: { code: "NETWORK", message: "Network unavailable." },
    })
    await tick()
    Array.from(document.querySelectorAll("button"))
      .find((button) => button.textContent?.trim() === "Retry update")?.click()
    await flushAsync()

    expect(providerUpdateStreaming).toHaveBeenCalledTimes(2)
    expect(providerList).toHaveBeenCalled()
    expect(document.body.textContent).not.toContain("Update failed")
    unmount()
  })

  it("finishes the provider that started the update after navigation", async () => {
    const { rerender, unmount } = render(ProviderSheet, {
      props: { provider: makeProvider("ssh"), open: true },
    })
    await flushAsync()
    Array.from(document.querySelectorAll("button"))
      .find((button) => button.textContent?.trim() === "Update")?.click()
    await tick()
    const dialog = Array.from(document.querySelectorAll("[role='dialog']")).find(
      (node) => node.textContent?.includes("Update 'ssh' to 0.2.0"),
    )
    Array.from(dialog?.querySelectorAll("button") ?? [])
      .find((button) => button.textContent?.trim() === "Update")?.click()
    await flushAsync()

    await rerender({ provider: makeProvider("docker"), open: true })
    await flushAsync()
    progressCallback?.({
      commandId: "update-command-1",
      message: "ssh update complete",
      success: true,
      done: true,
    })
    await flushAsync()

    expect(loadVersionsFor).toHaveBeenCalledWith("ssh")
    expect(document.body.textContent).not.toContain("ssh update complete")
    expect(document.body.textContent).not.toContain("Updated docker")
    unmount()
  })

  it("shows refresh recovery when post-update synchronization fails", async () => {
    providerList.mockRejectedValueOnce(new Error("list unavailable"))
    const { unmount } = render(ProviderSheet, {
      props: { provider: makeProvider("ssh"), open: true },
    })
    await flushAsync()
    Array.from(document.querySelectorAll("button"))
      .find((button) => button.textContent?.trim() === "Update")?.click()
    await tick()
    const dialog = Array.from(document.querySelectorAll("[role='dialog']")).find(
      (node) => node.textContent?.includes("Update 'ssh' to 0.2.0"),
    )
    Array.from(dialog?.querySelectorAll("button") ?? [])
      .find((button) => button.textContent?.trim() === "Update")?.click()
    await flushAsync()
    progressCallback?.({
      commandId: "update-command-1",
      success: true,
      done: true,
    })
    await flushAsync()

    expect(document.body.textContent).toContain("Status may be out of date")
    expect(document.body.textContent).toContain("list unavailable")
    expect(document.body.textContent).toContain("Refresh status")
    expect(document.body.textContent).not.toContain("ssh is ready to use")
    unmount()
  })

  it("hydrates update failure recovery from the provider job store", async () => {
    providerJobsBox.store.set({
      ssh: {
        activity: "updating",
        phase: "failed",
        state: "failed",
        error: "The download timed out.",
        errorCode: "NETWORK",
        logs: ["download: connection timed out"],
      },
    })
    const { unmount } = render(ProviderSheet, {
      props: { provider: makeProvider("ssh"), open: true },
    })
    await flushAsync()

    expect(document.body.textContent).toContain("Update failed")
    expect(document.body.textContent).toContain("The download timed out.")
    expect(document.body.textContent).toContain("Retry update")
    expect(document.body.textContent).toContain("download: connection timed out")
    unmount()
  })


  it("refreshes provider state without repeating a completed update", async () => {
    providerJobsBox.store.set({
      ssh: {
        activity: "updating",
        phase: "failed",
        state: "failed",
        error: "The provider updated, but its current state could not be refreshed.",
        errorCode: "provider_refresh_failed",
        logs: ["Provider update complete"],
      },
    })
    const { unmount } = render(ProviderSheet, {
      props: { provider: makeProvider("ssh"), open: true },
    })
    await flushAsync()

    expect(document.body.textContent).toContain("Status may be out of date")
    expect(document.body.textContent).toContain("Provider update complete")
    expect(document.body.textContent).not.toContain("Retry update")
    Array.from(document.querySelectorAll("button"))
      .find((button) => button.textContent?.trim() === "Refresh status")?.click()
    await flushAsync()
    expect(providerRefreshState).toHaveBeenCalledWith("ssh")
    expect(providerUpdateStreaming).not.toHaveBeenCalled()
    unmount()
  })

  it("keeps refresh recovery visible when version reload fails", async () => {
    providerJobsBox.store.set({
      ssh: {
        activity: "updating",
        phase: "failed",
        state: "failed",
        error: "The provider updated, but its current state could not be refreshed.",
        errorCode: "provider_refresh_failed",
      },
    })
    loadVersionsFor.mockRejectedValueOnce(new Error("versions unavailable"))
    const { unmount } = render(ProviderSheet, {
      props: { provider: makeProvider("ssh"), open: true },
    })
    await flushAsync()

    Array.from(document.querySelectorAll("button"))
      .find((button) => button.textContent?.trim() === "Refresh status")?.click()
    await flushAsync()

    expect(loadVersionsFor).toHaveBeenCalledWith("ssh")
    expect(document.body.textContent).toContain("Status may be out of date")
    unmount()
  })

})
