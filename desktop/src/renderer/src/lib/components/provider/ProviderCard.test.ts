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
import {
  initializingProviders,
  markInitializing,
  clearInitializing,
} from "$lib/stores/providers.js"

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
    initializingProviders.set(new Set())
    markInitializing("ssh")
    const { container, unmount } = render(ProviderCard, {
      props: { provider: makeProvider("ssh", { state: { initialized: false } }) },
    })

    const text = container.textContent ?? ""
    expect(text.toLowerCase()).toContain("initializing")
    expect(text.toLowerCase()).not.toContain("not initialized")
    clearInitializing("ssh")
    unmount()
  })

  it("shows not initialized when no init is in flight", () => {
    initializingProviders.set(new Set())
    const { container, unmount } = render(ProviderCard, {
      props: { provider: makeProvider("ssh", { state: { initialized: false } }) },
    })

    expect((container.textContent ?? "").toLowerCase()).toContain(
      "not initialized",
    )
    unmount()
  })
})
