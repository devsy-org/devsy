import { cleanup, fireEvent, render, waitFor } from "@testing-library/svelte"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { activeContext } from "$lib/stores/contexts.js"
import { effectiveTheme } from "$lib/stores/settings.js"
import { clearProviderIconLoader } from "$lib/utils/provider-icon-loader.js"
import type { ProviderIconReply } from "$shared/provider-icon.js"
import darkSVG from "../../../../../../test-fixtures/provider-icons/assets/dark.svg?raw"
import lightSVG from "../../../../../../test-fixtures/provider-icons/assets/light.svg?raw"

const { providerIcon } = vi.hoisted(() => ({ providerIcon: vi.fn() }))

vi.mock("$lib/ipc/commands.js", () => ({ providerIcon }))

import ProviderIcon from "./ProviderIcon.svelte"

const LIGHT = `data:image/svg+xml;base64,${btoa(lightSVG)}`
const DARK = `data:image/svg+xml;base64,${btoa(darkSVG)}`
const PLACEHOLDER = "./icons/providers/provider_placeholder.svg"
const DOCKER = "./icons/providers/docker.svg"
const LIMA = "./icons/providers/lima.svg"

/** Answers provider_icon requests per theme; a variant not listed is missing. */
function serveIcons(
  revision: string,
  images: Partial<Record<"light" | "dark", string | Error>>,
) {
  providerIcon.mockImplementation(
    async ({
      theme,
    }: {
      theme: "light" | "dark"
    }): Promise<ProviderIconReply> => {
      const image = images[theme]
      if (image instanceof Error) throw image
      if (image === undefined) return { revision, missing: true }
      return { revision, dataUrl: image }
    },
  )
}

function image(container: HTMLElement): HTMLImageElement {
  const img = container.querySelector("img")
  if (!img) throw new Error("no <img> rendered")
  return img
}

const custom = {
  name: "harvester",
  icon: "assets/light.svg",
  iconDark: "assets/dark.svg",
  iconRevision: "rev-a",
}

describe("ProviderIcon", () => {
  beforeEach(() => {
    providerIcon.mockReset()
    clearProviderIconLoader()
    activeContext.set("work")
    effectiveTheme.set("light")
  })

  afterEach(() => {
    cleanup()
  })

  describe("without provider-owned icons", () => {
    it("keeps the bundled mapping for name-only catalog entries", () => {
      const { container } = render(ProviderIcon, { props: { name: "docker" } })

      expect(image(container).getAttribute("src")).toBe(DOCKER)
      expect(providerIcon).not.toHaveBeenCalled()
    })

    it("uses the placeholder for unknown names", () => {
      const { container } = render(ProviderIcon, {
        props: { name: "something-else" },
      })

      expect(image(container).getAttribute("src")).toBe(PLACEHOLDER)
    })

    it("does not request an image when the provider has no icon revision", () => {
      const { container } = render(ProviderIcon, {
        props: {
          name: "harvester",
          icon: "assets/light.svg",
          iconDark: "assets/dark.svg",
        },
      })

      expect(image(container).getAttribute("src")).toBe(PLACEHOLDER)
      expect(providerIcon).not.toHaveBeenCalled()
    })
  })

  describe("light theme", () => {
    it("shows the custom light image and asks only for the light variant", async () => {
      serveIcons("rev-a", { light: LIGHT, dark: DARK })
      const { container } = render(ProviderIcon, { props: custom })

      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(LIGHT),
      )
      expect(providerIcon).toHaveBeenCalledTimes(1)
      expect(providerIcon).toHaveBeenCalledWith({
        providerName: "harvester",
        context: "work",
        theme: "light",
        expectedRevision: "rev-a",
      })
    })

    it("never shows the dark image, falling back to the bundled mapping", async () => {
      serveIcons("rev-a", { dark: DARK })
      const { container } = render(ProviderIcon, {
        props: { ...custom, name: "docker-cloud" },
      })

      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(DOCKER),
      )
    })
  })

  describe("dark theme", () => {
    beforeEach(() => effectiveTheme.set("dark"))

    it("prefers the custom dark image", async () => {
      serveIcons("rev-a", { light: LIGHT, dark: DARK })
      const { container } = render(ProviderIcon, { props: custom })

      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(DARK),
      )
    })

    it("falls back to the custom light image when the dark variant is missing", async () => {
      serveIcons("rev-a", { light: LIGHT })
      const { container } = render(ProviderIcon, { props: custom })

      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(LIGHT),
      )
    })

    it("uses the light image when the manifest declares no dark icon", async () => {
      serveIcons("rev-a", { light: LIGHT })
      const { container } = render(ProviderIcon, {
        props: { ...custom, iconDark: "" },
      })

      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(LIGHT),
      )
      expect(providerIcon).toHaveBeenCalledTimes(1)
    })

    it("falls back to the bundled mapping and then the placeholder when both variants are unusable", async () => {
      serveIcons("rev-a", { light: new Error("disk"), dark: new Error("disk") })
      const mapped = render(ProviderIcon, {
        props: { ...custom, name: "my-docker-fleet" },
      })
      await waitFor(() =>
        expect(image(mapped.container).getAttribute("src")).toBe(DOCKER),
      )
      mapped.unmount()

      const unmapped = render(ProviderIcon, { props: custom })
      await waitFor(() =>
        expect(image(unmapped.container).getAttribute("src")).toBe(PLACEHOLDER),
      )
    })
  })

  describe("custom registration names", () => {
    it("shows the bundled fallback while loading, then lets the custom image win", async () => {
      const pending = Promise.withResolvers<ProviderIconReply>()
      providerIcon.mockImplementation(() => pending.promise)
      const { container } = render(ProviderIcon, {
        props: { ...custom, name: "my-docker-fleet" },
      })

      expect(image(container).getAttribute("src")).toBe(DOCKER)
      pending.resolve({ revision: "rev-a", dataUrl: LIGHT })

      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(LIGHT),
      )
    })

    it("still falls back to the substring match when the custom image is missing", async () => {
      serveIcons("rev-a", {})
      const { container } = render(ProviderIcon, {
        props: { ...custom, name: "my-docker-fleet" },
      })

      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(DOCKER),
      )
    })
  })

  describe("live updates", () => {
    it("switches between light and dark images when the effective theme changes", async () => {
      serveIcons("rev-a", { light: LIGHT, dark: DARK })
      const { container } = render(ProviderIcon, { props: custom })
      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(LIGHT),
      )

      effectiveTheme.set("dark")
      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(DARK),
      )

      effectiveTheme.set("light")
      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(LIGHT),
      )
    })

    it("does not show a previous revision's image while the new revision loads", async () => {
      serveIcons("rev-a", { light: LIGHT })
      const { container, rerender } = render(ProviderIcon, { props: custom })
      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(LIGHT),
      )

      const next = Promise.withResolvers<ProviderIconReply>()
      providerIcon.mockImplementation(() => next.promise)
      await rerender({ ...custom, iconRevision: "rev-b" })

      expect(image(container).getAttribute("src")).not.toBe(LIGHT)
      expect(image(container).getAttribute("src")).toBe(PLACEHOLDER)

      next.resolve({ revision: "rev-b", dataUrl: DARK })
      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(DARK),
      )
    })

    it("drops a delayed response that arrives after the provider was renamed", async () => {
      const first = Promise.withResolvers<ProviderIconReply>()
      providerIcon.mockImplementationOnce(() => first.promise)
      const { container, rerender } = render(ProviderIcon, { props: custom })

      serveIcons("rev-a", { light: DARK })
      await rerender({ ...custom, name: "renamed" })
      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(DARK),
      )

      first.resolve({ revision: "rev-a", dataUrl: LIGHT })
      await first.promise
      await Promise.resolve()

      expect(image(container).getAttribute("src")).toBe(DARK)
    })

    it("bounds repeated stale replies and leaves unavailable lookups uncached", async () => {
      let requests = 0
      providerIcon.mockImplementation(() => {
        requests += 1
        if (requests <= 2) {
          return Promise.resolve({ revision: "rev-b", stale: true })
        }
        return Promise.resolve({ revision: "rev-a", dataUrl: LIGHT })
      })
      const unavailable = render(ProviderIcon, { props: custom })

      await waitFor(() => expect(requests).toBe(2))
      expect(image(unavailable.container).getAttribute("src")).toBe(PLACEHOLDER)
      unavailable.unmount()

      const recovered = render(ProviderIcon, { props: custom })
      await waitFor(() =>
        expect(image(recovered.container).getAttribute("src")).toBe(LIGHT),
      )
      expect(requests).toBe(3)
    })

    it("re-requests images after the active context changes", async () => {
      serveIcons("rev-a", { light: LIGHT })
      const { container } = render(ProviderIcon, { props: custom })
      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(LIGHT),
      )

      activeContext.set("other")
      await waitFor(() => expect(providerIcon).toHaveBeenCalledTimes(2))
      expect(providerIcon).toHaveBeenLastCalledWith(
        expect.objectContaining({ context: "other" }),
      )
    })

    it("recovers an icon after an A-B-A switch during a pending read", async () => {
      const firstA = Promise.withResolvers<ProviderIconReply>()
      const freshA = Promise.withResolvers<ProviderIconReply>()
      const freshAStarted = Promise.withResolvers<void>()
      let contextACalls = 0
      providerIcon.mockImplementation(
        ({ context }: { context: string }): Promise<ProviderIconReply> => {
          if (context === "work") {
            contextACalls += 1
            if (contextACalls === 1) return firstA.promise
            freshAStarted.resolve()
            return freshA.promise
          }
          return Promise.resolve({ revision: "rev-a", dataUrl: DARK })
        },
      )
      const { container } = render(ProviderIcon, { props: custom })

      activeContext.set("other")
      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(DARK),
      )

      const paintedSources: string[] = []
      const observer = new MutationObserver((records) => {
        for (const record of records) {
          if (record.attributeName === "src") {
            paintedSources.push(
              (record.target as HTMLImageElement).getAttribute("src") ?? "",
            )
          }
        }
      })
      observer.observe(container, {
        attributes: true,
        subtree: true,
        attributeFilter: ["src"],
      })

      activeContext.set("work")
      firstA.resolve({ revision: "rev-a", dataUrl: DARK, stale: true })

      await freshAStarted.promise
      await firstA.promise
      await Promise.resolve()
      await Promise.resolve()
      expect(paintedSources).not.toContain(DARK)
      observer.disconnect()

      freshA.resolve({ revision: "rev-a", dataUrl: LIGHT })
      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(LIGHT),
      )
    })

    it("reloads the icon after a pending provider is deleted and re-added", async () => {
      const revokedRequest = Promise.withResolvers<ProviderIconReply>()
      const firstRequestStarted = Promise.withResolvers<void>()
      let requests = 0
      providerIcon.mockImplementation(() => {
        requests += 1
        if (requests === 1) {
          firstRequestStarted.resolve()
          return revokedRequest.promise
        }
        return Promise.resolve({ revision: "rev-a", dataUrl: LIGHT })
      })
      const removed = render(ProviderIcon, { props: custom })
      await firstRequestStarted.promise
      removed.unmount()
      const current = render(ProviderIcon, { props: custom })
      const paintedSources: string[] = []
      const observer = new MutationObserver((records) => {
        for (const record of records) {
          if (record.attributeName === "src") {
            paintedSources.push(
              (record.target as HTMLImageElement).getAttribute("src") ?? "",
            )
          }
        }
      })
      observer.observe(current.container, {
        attributes: true,
        subtree: true,
        attributeFilter: ["src"],
      })

      revokedRequest.resolve({
        revision: "rev-a",
        dataUrl: DARK,
        stale: true,
      })
      await waitFor(() =>
        expect(image(current.container).getAttribute("src")).toBe(LIGHT),
      )
      await Promise.resolve()
      await Promise.resolve()
      expect(paintedSources).not.toContain(DARK)
      observer.disconnect()
    })

    it("recovers a remounted icon after other lookups evict its pending result", async () => {
      const revokedRequest = Promise.withResolvers<ProviderIconReply>()
      const firstRequestStarted = Promise.withResolvers<void>()
      let originalRequests = 0
      providerIcon.mockImplementation(
        ({ providerName }: { providerName: string }) => {
          if (providerName === custom.name) {
            originalRequests += 1
            if (originalRequests === 1) {
              firstRequestStarted.resolve()
              return revokedRequest.promise
            }
          }
          return Promise.resolve({ revision: "rev-a", dataUrl: LIGHT })
        },
      )
      const removed = render(ProviderIcon, { props: custom })
      await firstRequestStarted.promise
      removed.unmount()
      const current = render(ProviderIcon, { props: custom })
      const paintedSources: string[] = []
      const observer = new MutationObserver((records) => {
        for (const record of records) {
          if (record.attributeName === "src") {
            paintedSources.push(
              (record.target as HTMLImageElement).getAttribute("src") ?? "",
            )
          }
        }
      })
      observer.observe(current.container, {
        attributes: true,
        subtree: true,
        attributeFilter: ["src"],
      })

      const otherIcons = Array.from({ length: 64 }, (_, index) =>
        render(ProviderIcon, {
          props: { ...custom, name: `other-${index}` },
        }),
      )
      await waitFor(() => {
        for (const other of otherIcons) {
          expect(image(other.container).getAttribute("src")).toBe(LIGHT)
        }
      })

      revokedRequest.resolve({
        revision: "rev-a",
        dataUrl: DARK,
        stale: true,
      })
      await waitFor(() =>
        expect(image(current.container).getAttribute("src")).toBe(LIGHT),
      )
      await Promise.resolve()
      await Promise.resolve()
      expect(paintedSources).not.toContain(DARK)
      observer.disconnect()
    })

    it("shares a pending lookup across cards without leaking another revision", async () => {
      const pending = Promise.withResolvers<ProviderIconReply>()
      providerIcon.mockImplementation(() => pending.promise)
      const first = render(ProviderIcon, { props: custom })
      const second = render(ProviderIcon, { props: custom })
      expect(providerIcon).toHaveBeenCalledTimes(1)

      serveIcons("rev-b", { light: DARK })
      const replacement = render(ProviderIcon, {
        props: { ...custom, iconRevision: "rev-b" },
      })
      await waitFor(() =>
        expect(image(replacement.container).getAttribute("src")).toBe(DARK),
      )

      pending.resolve({ revision: "rev-a", dataUrl: LIGHT })
      await waitFor(() => {
        expect(image(first.container).getAttribute("src")).toBe(LIGHT)
        expect(image(second.container).getAttribute("src")).toBe(LIGHT)
        expect(image(replacement.container).getAttribute("src")).toBe(DARK)
      })
    })
  })

  describe("failed images", () => {
    it("advances one candidate per failure and never requests a broken source again", async () => {
      effectiveTheme.set("dark")
      serveIcons("rev-a", { light: LIGHT, dark: DARK })
      const { container } = render(ProviderIcon, {
        props: { ...custom, name: "my-docker-fleet" },
      })
      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(DARK),
      )
      const requests = providerIcon.mock.calls.length

      await fireEvent.error(image(container))
      expect(image(container).getAttribute("src")).toBe(LIGHT)

      await fireEvent.error(image(container))
      expect(image(container).getAttribute("src")).toBe(DOCKER)

      await fireEvent.error(image(container))
      expect(image(container).getAttribute("src")).toBe(PLACEHOLDER)

      // Errors on the placeholder are terminal: no loop, no new request.
      await fireEvent.error(image(container))
      await fireEvent.error(image(container))
      expect(image(container).getAttribute("src")).toBe(PLACEHOLDER)
      expect(providerIcon).toHaveBeenCalledTimes(requests)
    })

    it("advances a broken bundled fallback while the custom request is pending", async () => {
      const pending = Promise.withResolvers<ProviderIconReply>()
      providerIcon.mockImplementation(() => pending.promise)
      const { container } = render(ProviderIcon, {
        props: { ...custom, name: "my-docker-fleet" },
      })
      expect(image(container).getAttribute("src")).toBe(DOCKER)

      await fireEvent.error(image(container))
      expect(image(container).getAttribute("src")).toBe(PLACEHOLDER)
      await fireEvent.error(image(container))
      expect(image(container).getAttribute("src")).toBe(PLACEHOLDER)

      pending.resolve({ revision: "rev-a", dataUrl: LIGHT })
      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(LIGHT),
      )
    })

    it("resets failure state when the revision changes", async () => {
      serveIcons("rev-a", { light: LIGHT })
      const { container, rerender } = render(ProviderIcon, { props: custom })
      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(LIGHT),
      )
      await fireEvent.error(image(container))
      expect(image(container).getAttribute("src")).toBe(PLACEHOLDER)

      serveIcons("rev-b", { light: LIGHT })
      await rerender({ ...custom, iconRevision: "rev-b" })

      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(LIGHT),
      )
    })

    it("resets failure state when the effective theme changes", async () => {
      serveIcons("rev-a", { light: LIGHT, dark: DARK })
      const { container } = render(ProviderIcon, { props: custom })
      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(LIGHT),
      )
      await fireEvent.error(image(container))
      expect(image(container).getAttribute("src")).toBe(PLACEHOLDER)

      effectiveTheme.set("dark")

      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(DARK),
      )
    })
  })

  describe("Lima inversion", () => {
    it("inverts the bundled Lima logo", () => {
      const { container } = render(ProviderIcon, { props: { name: "lima" } })

      expect(image(container).getAttribute("src")).toBe(LIMA)
      expect(image(container).classList.contains("dark:invert")).toBe(true)
    })

    it("never inverts a supplied custom image, even for a Lima-named provider", async () => {
      effectiveTheme.set("dark")
      serveIcons("rev-a", { dark: DARK })
      const { container } = render(ProviderIcon, {
        props: { ...custom, name: "lima-fleet" },
      })

      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(DARK),
      )
      expect(image(container).classList.contains("dark:invert")).toBe(false)
    })

    it("inverts again once the custom image fails and the bundled Lima logo is selected", async () => {
      serveIcons("rev-a", { light: LIGHT })
      const { container } = render(ProviderIcon, {
        props: { ...custom, name: "lima-fleet" },
      })
      await waitFor(() =>
        expect(image(container).getAttribute("src")).toBe(LIGHT),
      )

      await fireEvent.error(image(container))

      expect(image(container).getAttribute("src")).toBe(LIMA)
      expect(image(container).classList.contains("dark:invert")).toBe(true)
    })
  })

  it("renders only an <img> with alt text for downloaded content", async () => {
    serveIcons("rev-a", { light: LIGHT })
    const { container } = render(ProviderIcon, { props: custom })
    await waitFor(() =>
      expect(image(container).getAttribute("src")).toBe(LIGHT),
    )

    expect(image(container).getAttribute("alt")).toBe("harvester icon")
    expect(container.querySelector("object, embed, iframe, svg")).toBeNull()
  })
})
