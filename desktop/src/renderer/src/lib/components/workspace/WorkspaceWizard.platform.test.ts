import { tick } from "svelte"
import { render, fireEvent, cleanup, waitFor } from "@testing-library/svelte"
import { describe, it, expect, vi, afterEach, beforeEach } from "vitest"
import type { Provider } from "$lib/types/index.js"

const getImagePlatforms = vi.fn()
const getHostPlatform = vi.fn().mockResolvedValue("linux/arm64")
const workspaceUp = vi.fn().mockResolvedValue("cmd-1")

vi.mock("$lib/ipc/commands.js", () => ({
  getImagePlatforms: (...args: unknown[]) => getImagePlatforms(...args),
  getHostPlatform: (...args: unknown[]) => getHostPlatform(...args),
  workspaceUp: (...args: unknown[]) => workspaceUp(...args),
  openDirectoryDialog: vi.fn(),
}))
vi.mock("$lib/ipc/events.js", () => ({
  onCommandProgress: vi.fn().mockResolvedValue(() => {}),
}))

vi.mock("$lib/stores/imageCatalog.js", async () => {
  const { writable } = await import("svelte/store")
  const actual = await vi.importActual<
    typeof import("$lib/stores/imageCatalog.js")
  >("$lib/stores/imageCatalog.js")
  return {
    imageCatalog: writable({ images: [], categories: [], loading: false }),
    loadImageCatalog: vi.fn(),
    filterImages: actual.filterImages,
    isImageCompatible: actual.isImageCompatible,
  }
})

vi.mock("$lib/stores/providers.js", async () => {
  const { writable } = await import("svelte/store")
  return { providers: writable<Provider[]>([]) }
})
vi.mock("$lib/stores/workspaces.js", async () => {
  const { writable } = await import("svelte/store")
  return { workspaces: writable<{ id: string }[]>([]) }
})
vi.mock("$lib/stores/toasts.js", () => ({
  toasts: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}))
vi.mock("$lib/router.js", () => ({
  goto: vi.fn(),
  push: vi.fn(),
  replace: vi.fn(),
  router: {},
  location: { subscribe: () => () => {} },
}))

import { providers } from "$lib/stores/providers.js"
import { workspaces } from "$lib/stores/workspaces.js"
import WorkspaceWizard from "./WorkspaceWizard.svelte"

function makeProvider(name: string, initialized = true): Provider {
  return { name, version: "0.1.0", state: { initialized } }
}

async function flushAsync() {
  await new Promise((r) => setTimeout(r, 30))
  await tick()
  await tick()
}

function getActiveContinue(): HTMLButtonElement {
  return Array.from(document.querySelectorAll("button")).find(
    (b) =>
      b.textContent?.trim() === "Continue" &&
      !(b as HTMLButtonElement).disabled,
  ) as HTMLButtonElement
}

function getLaunch(): HTMLButtonElement {
  return Array.from(document.querySelectorAll("button")).find(
    (b) => b.textContent?.trim() === "Launch",
  ) as HTMLButtonElement
}

// Drive the wizard from the provider step to the review step with a custom
// image reference, which is the only path that exercises the platform check.
async function gotoReviewWithImage(
  getByText: (t: string) => HTMLElement,
  imageRef: string,
) {
  // provider step
  await fireEvent.click(getByText("docker"))
  await flushAsync()
  await fireEvent.click(getActiveContinue())
  await flushAsync()

  // source step: switch to the Image tab and enter a custom ref
  await fireEvent.click(getByText("Image"))
  await flushAsync()
  const customImageInput = document.querySelector(
    'input[placeholder*="registry/image:tag"]',
  ) as HTMLInputElement
  await fireEvent.input(customImageInput, { target: { value: imageRef } })
  await flushAsync()
  await fireEvent.click(getActiveContinue())
  await flushAsync()

  // ide step
  await fireEvent.click(getActiveContinue())
  await flushAsync()
}

describe("WorkspaceWizard platform compatibility", () => {
  beforeEach(() => {
    workspaceUp.mockClear()
    getImagePlatforms.mockReset()
    getHostPlatform.mockReset().mockResolvedValue("linux/arm64")
    workspaceUp.mockResolvedValue("cmd-1")
    providers.set([makeProvider("docker")])
    workspaces.set([])
  })

  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
    document.body.innerHTML = ""
  })

  it("warns and offers emulation for an amd64-only image on an arm64 host", async () => {
    getImagePlatforms.mockResolvedValue(["linux/amd64"])
    const { getByText, unmount } = render(WorkspaceWizard, {
      props: { open: true },
    })
    await flushAsync()
    await gotoReviewWithImage(getByText, "ubuntu:22.04")

    // Warning renders once the (mocked) platform lookup resolves.
    await waitFor(() =>
      expect(getByText(/no build for your machine/i)).toBeTruthy(),
    )

    const checkbox = document.querySelector(
      'input[type="checkbox"]',
    ) as HTMLInputElement
    expect(checkbox).toBeTruthy()

    await fireEvent.click(checkbox)
    await flushAsync()

    await fireEvent.click(getLaunch())
    await flushAsync()

    expect(workspaceUp).toHaveBeenCalledWith(
      expect.objectContaining({ platform: "linux/amd64" }),
    )
    unmount()
  })

  it("does not warn for a multi-arch image on an arm64 host", async () => {
    getImagePlatforms.mockResolvedValue(["linux/amd64", "linux/arm64"])
    const { getByText, queryByText, unmount } = render(WorkspaceWizard, {
      props: { open: true },
    })
    await flushAsync()
    await gotoReviewWithImage(getByText, "ubuntu:22.04")

    // Give the lookup a chance to resolve, then assert nothing surfaced.
    await flushAsync()
    expect(queryByText(/no build for your machine/i)).toBeNull()
    expect(document.querySelector('input[type="checkbox"]')).toBeNull()
    unmount()
  })

  it("stays silent and still launches when the platform lookup rejects", async () => {
    getImagePlatforms.mockRejectedValue(new Error("registry unreachable"))
    const { getByText, queryByText, unmount } = render(WorkspaceWizard, {
      props: { open: true },
    })
    await flushAsync()
    await gotoReviewWithImage(getByText, "ubuntu:22.04")

    await flushAsync()
    expect(queryByText(/no build for your machine/i)).toBeNull()
    expect(document.querySelector('input[type="checkbox"]')).toBeNull()

    await fireEvent.click(getLaunch())
    await flushAsync()

    expect(workspaceUp).toHaveBeenCalled()
    expect(workspaceUp).toHaveBeenCalledWith(
      expect.objectContaining({ platform: undefined }),
    )
    unmount()
  })
})
