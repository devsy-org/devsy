import { render, waitFor } from "@testing-library/svelte"
import { describe, expect, it, vi } from "vitest"
import type * as Commands from "$lib/ipc/commands.js"
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

const { providerIcon } = vi.hoisted(() => ({ providerIcon: vi.fn() }))

vi.mock("$lib/ipc/commands.js", async (importOriginal) => ({
  ...(await importOriginal<typeof Commands>()),
  providerIcon,
}))

import { providerJobs } from "$lib/stores/providers.js"
import ProviderCard from "./ProviderCard.svelte"

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

  it("shows the configured source pin separately from the installed version", () => {
    const source = `github.com/example/${"long-provider-name-".repeat(8)}@v1.2.3`
    const { getByText, unmount } = render(ProviderCard, {
      props: {
        provider: makeProvider("custom-provider", {
          source: { raw: source, github: "example/provider" },
        }),
      },
    })
    expect(getByText(source)).toBeDefined()
    expect(getByText("Source")).toBeDefined()
    expect(getByText("Version 0.1.0")).toBeDefined()
    unmount()
  })

  it("shows the deprecation badge only when the provider flag is set", () => {
    const deprecated = render(ProviderCard, {
      props: {
        provider: makeProvider("custom-builtin-name", { deprecated: true }),
      },
    })
    expect(deprecated.getByText("Deprecated built-in")).toBeDefined()
    deprecated.unmount()

    const external = render(ProviderCard, {
      props: {
        provider: makeProvider("microsandbox", {
          source: { github: "example/provider" },
          deprecated: false,
        }),
      },
    })
    expect(external.queryByText("Deprecated built-in")).toBeNull()
    external.unmount()
  })

  it("shows the initializing badge while an uninitialized provider is in flight", () => {
    providerJobs.set({
      ssh: { activity: "initializing", phase: "running_init" },
    })
    const { container, unmount } = render(ProviderCard, {
      props: {
        provider: makeProvider("ssh", { state: { initialized: false } }),
      },
    })

    const text = container.textContent ?? ""
    expect(text.toLowerCase()).toContain("initializing")
    expect(text.toLowerCase()).not.toContain("not initialized")
    providerJobs.set({})
    unmount()
  })

  it("shows installing rather than not initialized during install", () => {
    providerJobs.set({
      ssh: { activity: "installing", phase: "installing_provider" },
    })
    const { container, unmount } = render(ProviderCard, {
      props: {
        provider: makeProvider("ssh", { state: { initialized: false } }),
      },
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
      props: {
        provider: makeProvider("ssh", { state: { initialized: false } }),
      },
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
      props: {
        provider: makeProvider("ssh", { state: { initialized: false } }),
      },
    })

    expect((container.textContent ?? "").toLowerCase()).toContain(
      "not initialized",
    )
    unmount()
  })

  it("renders the provider's own icon from the installed icon metadata", async () => {
    const dataUrl = "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4="
    providerIcon.mockResolvedValue({ revision: "rev-a", dataUrl })
    const { container, unmount } = render(ProviderCard, {
      props: {
        provider: makeProvider("my-docker-fleet", {
          icon: "assets/logo.svg",
          iconRevision: "rev-a",
        }),
      },
    })

    await waitFor(() =>
      expect(container.querySelector("img")?.getAttribute("src")).toBe(dataUrl),
    )
    unmount()
  })
})
