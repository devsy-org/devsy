import { render } from "@testing-library/svelte"
import { describe, expect, it, vi } from "vitest"
import type { Provider } from "$lib/types/index.js"

vi.mock("$lib/stores/providerVersions.js", async () => {
  const { writable } = await import("svelte/store")
  return {
    providerVersions: writable({
      byProvider: {},
      updates: {},
      lastCheckedAt: null,
    }),
  }
})

import ProviderCard from "./ProviderCard.svelte"
import { providerJobs } from "$lib/stores/providers.js"

function makeProvider(name: string, extras: Partial<Provider> = {}): Provider {
  return {
    name,
    version: "0.1.0",
    state: { initialized: true },
    ...extras,
  }
}

describe("ProviderCard", () => {
  it("renders the Default pill with a star icon when provider.isDefault is true", () => {
    const { container, unmount } = render(ProviderCard, {
      props: { provider: makeProvider("ssh", { isDefault: true }) },
    })

    const pill = Array.from(container.querySelectorAll("span")).find((el) =>
      el.textContent?.trim().toLowerCase().includes("default"),
    )
    expect(pill).toBeDefined()
    expect(pill?.querySelector("svg")).not.toBeNull()
    unmount()
  })

  it("does not render the Default pill when provider.isDefault is false", () => {
    const { container, unmount } = render(ProviderCard, {
      props: { provider: makeProvider("ssh", { isDefault: false }) },
    })

    const pill = Array.from(container.querySelectorAll("span")).find(
      (el) => el.textContent?.trim().toLowerCase() === "default",
    )
    expect(pill).toBeUndefined()
    unmount()
  })

  it("shows the initializing badge while an uninitialized provider is in flight", () => {
    providerJobs.set({ ssh: { activity: "initializing", phase: "running_init" } })
    const { container, unmount } = render(ProviderCard, {
      props: { provider: makeProvider("ssh", { state: { initialized: false } }) },
    })

    const text = container.textContent ?? ""
    expect(text.toLowerCase()).toContain("initializing")
    expect(text.toLowerCase()).not.toContain("not initialized")
    providerJobs.set({})
    unmount()
  })

  // The original bug: `provider add` persists the provider before init runs,
  // so the watcher reports initialized:false while the install is in flight.
  it("shows installing rather than not initialized during install", () => {
    providerJobs.set({
      ssh: { activity: "installing", phase: "installing_provider" },
    })
    const { container, unmount } = render(ProviderCard, {
      props: { provider: makeProvider("ssh", { state: { initialized: false } }) },
    })

    const text = (container.textContent ?? "").toLowerCase()
    expect(text).toContain("installing")
    expect(text).not.toContain("not initialized")
    providerJobs.set({})
    unmount()
  })

  it("shows a failure badge when the job recorded an error", () => {
    providerJobs.set({
      ssh: { activity: "initializing", phase: "failed", error: "init: boom" },
    })
    const { container, unmount } = render(ProviderCard, {
      props: { provider: makeProvider("ssh", { state: { initialized: false } }) },
    })

    const text = (container.textContent ?? "").toLowerCase()
    expect(text).toContain("failed")
    expect(text).not.toContain("not initialized")
    providerJobs.set({})
    unmount()
  })

  it("shows not initialized when no job is in flight", () => {
    providerJobs.set({})
    const { container, unmount } = render(ProviderCard, {
      props: { provider: makeProvider("ssh", { state: { initialized: false } }) },
    })

    expect((container.textContent ?? "").toLowerCase()).toContain(
      "not initialized",
    )
    unmount()
  })
})
