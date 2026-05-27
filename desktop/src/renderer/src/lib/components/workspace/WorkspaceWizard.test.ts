import { tick } from "svelte"
import { fireEvent, render } from "@testing-library/svelte"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import type { Provider, CommandProgress } from "$lib/types/index.js"

const workspaceUp = vi.fn()
const onCommandProgress = vi.fn()

vi.mock("$lib/ipc/commands.js", () => ({
  workspaceUp: (...args: unknown[]) => workspaceUp(...args),
}))

vi.mock("$lib/ipc/events.js", () => ({
  onCommandProgress: (...args: unknown[]) => onCommandProgress(...args),
}))

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

let progressCallback: ((progress: CommandProgress) => void) | null = null

async function advanceToReview(getByText: (t: string) => HTMLElement) {
  // provider
  await fireEvent.click(getByText("docker"))
  await flushAsync()
  await fireEvent.click(getAllContinue(getByText)[0])
  await flushAsync()
  // source: choose template (sets source)
  await fireEvent.click(getByText("Python"))
  await flushAsync()
  await fireEvent.click(getActiveContinue(getByText))
  await flushAsync()
  // ide
  await fireEvent.click(getActiveContinue(getByText))
  await flushAsync()
}

// Helpers
function getAllContinue(getByText: (t: string) => HTMLElement): HTMLElement[] {
  // Multiple "Continue" buttons can exist (advanced toggle text differs);
  // return a list and pick the first enabled non-disabled one.
  const all = Array.from(document.querySelectorAll("button")).filter(
    (b) => b.textContent?.trim() === "Continue",
  ) as HTMLElement[]
  return all
}

function getActiveContinue(_: (t: string) => HTMLElement): HTMLElement {
  const candidates = Array.from(document.querySelectorAll("button")).filter(
    (b) =>
      b.textContent?.trim() === "Continue" &&
      !(b as HTMLButtonElement).disabled,
  ) as HTMLElement[]
  return candidates[0]
}

describe("WorkspaceWizard", () => {
  beforeEach(() => {
    workspaceUp.mockReset()
    onCommandProgress.mockReset()
    providers.set([])
    workspaces.set([])
    progressCallback = null

    onCommandProgress.mockImplementation(
      async (cb: (progress: CommandProgress) => void) => {
        progressCallback = cb
        return () => {
          progressCallback = null
        }
      },
    )

    workspaceUp.mockImplementation(async () => {
      await new Promise((r) => setTimeout(r, 5))
      return "cmd-1"
    })
  })

  afterEach(() => {
    vi.clearAllMocks()
    document.body.innerHTML = ""
  })

  it("renders the provider step by default", async () => {
    providers.set([makeProvider("docker")])
    const { getByText, unmount } = render(WorkspaceWizard, {
      props: { open: true },
    })
    await flushAsync()
    expect(getByText("Choose a Provider")).toBeTruthy()
    unmount()
  })

  it("hard-blocks when there are zero initialized providers", async () => {
    providers.set([makeProvider("docker", false)])
    const { getByText, unmount } = render(WorkspaceWizard, {
      props: { open: true },
    })
    await flushAsync()
    expect(
      getByText(/At least one initialized provider is required/i),
    ).toBeTruthy()
    const continueBtn = Array.from(
      document.querySelectorAll("button"),
    ).find((b) => b.textContent?.trim() === "Continue") as HTMLButtonElement
    expect(continueBtn).toBeTruthy()
    expect(continueBtn.disabled).toBe(true)
    unmount()
  })

  it("only shows initialized providers on the provider step", async () => {
    providers.set([makeProvider("docker", true), makeProvider("ssh", false)])
    const { queryByText, unmount } = render(WorkspaceWizard, {
      props: { open: true },
    })
    await flushAsync()
    expect(queryByText("docker")).not.toBeNull()
    expect(queryByText("ssh")).toBeNull()
    unmount()
  })

  it("advances from provider to source after selecting and continuing", async () => {
    providers.set([makeProvider("docker")])
    const { getByText, unmount } = render(WorkspaceWizard, {
      props: { open: true },
    })
    await flushAsync()
    await fireEvent.click(getByText("docker"))
    await flushAsync()
    await fireEvent.click(getActiveContinue(getByText))
    await flushAsync()
    expect(getByText("Choose a Source")).toBeTruthy()
    unmount()
  })

  it("source step requires a non-empty source to continue", async () => {
    providers.set([makeProvider("docker")])
    const { getByText, unmount } = render(WorkspaceWizard, {
      props: { open: true },
    })
    await flushAsync()
    await fireEvent.click(getByText("docker"))
    await flushAsync()
    await fireEvent.click(getActiveContinue(getByText))
    await flushAsync()

    // On source step, with empty source, no Continue should be enabled
    const enabled = getActiveContinue(getByText)
    expect(enabled).toBeUndefined()
    unmount()
  })

  it("template selection populates the source field", async () => {
    providers.set([makeProvider("docker")])
    const { getByText, unmount } = render(WorkspaceWizard, {
      props: { open: true },
    })
    await flushAsync()
    await fireEvent.click(getByText("docker"))
    await flushAsync()
    await fireEvent.click(getActiveContinue(getByText))
    await flushAsync()

    await fireEvent.click(getByText("Python"))
    await flushAsync()

    const input = document.querySelector(
      'input[placeholder*="github.com/org/repo"]',
    ) as HTMLInputElement
    expect(input.value).toContain("vscode-remote-try-python")
    unmount()
  })

  it("review step shows summary and blocks Launch on name conflict", async () => {
    providers.set([makeProvider("docker")])
    workspaces.set([{ id: "python" }])
    const { getByText, unmount } = render(WorkspaceWizard, {
      props: { open: true },
    })
    await flushAsync()
    await advanceToReview(getByText)

    // Review heading + step indicator both contain "Review", so check the h2 specifically
    const heading = document.querySelector("h2")
    expect(heading?.textContent).toBe("Review")
    expect(getByText(/already exists/i)).toBeTruthy()
    const launchBtn = Array.from(
      document.querySelectorAll("button"),
    ).find((b) => b.textContent?.trim() === "Launch") as HTMLButtonElement
    expect(launchBtn.disabled).toBe(true)
    unmount()
  })

  it("registers the progress listener before awaiting workspaceUp", async () => {
    providers.set([makeProvider("docker")])
    let listenerRegisteredBeforeUp = false
    onCommandProgress.mockImplementation(async (cb) => {
      progressCallback = cb
      if (!workspaceUp.mock.calls.length) {
        listenerRegisteredBeforeUp = true
      }
      return () => {}
    })

    const { getByText, unmount } = render(WorkspaceWizard, {
      props: { open: true },
    })
    await flushAsync()
    await advanceToReview(getByText)

    const launchBtn = Array.from(
      document.querySelectorAll("button"),
    ).find((b) => b.textContent?.trim() === "Launch") as HTMLButtonElement
    await fireEvent.click(launchBtn)
    await flushAsync()

    expect(listenerRegisteredBeforeUp).toBe(true)
    expect(workspaceUp).toHaveBeenCalled()
    unmount()
  })

  it("streamed log lines appear in the launch step", async () => {
    providers.set([makeProvider("docker")])
    const { getByText, queryByText, unmount } = render(WorkspaceWizard, {
      props: { open: true },
    })
    await flushAsync()
    await advanceToReview(getByText)

    const launchBtn = Array.from(
      document.querySelectorAll("button"),
    ).find((b) => b.textContent?.trim() === "Launch") as HTMLButtonElement
    await fireEvent.click(launchBtn)
    await flushAsync()

    progressCallback?.({
      commandId: "cmd-1",
      message: "Building workspace...",
      done: false,
    } as CommandProgress)
    await flushAsync()

    expect(queryByText(/Building workspace/)).not.toBeNull()
    unmount()
  })

  it("shows Open Workspace on success", async () => {
    providers.set([makeProvider("docker")])
    const { getByText, queryByText, unmount } = render(WorkspaceWizard, {
      props: { open: true },
    })
    await flushAsync()
    await advanceToReview(getByText)

    const launchBtn = Array.from(
      document.querySelectorAll("button"),
    ).find((b) => b.textContent?.trim() === "Launch") as HTMLButtonElement
    await fireEvent.click(launchBtn)
    await flushAsync()

    progressCallback?.({
      commandId: "cmd-1",
      message: "Exit code: 0",
      done: true,
    } as CommandProgress)
    await flushAsync()

    expect(queryByText("Open Workspace")).not.toBeNull()
    unmount()
  })

  it("shows Retry on error", async () => {
    providers.set([makeProvider("docker")])
    const { getByText, queryByText, unmount } = render(WorkspaceWizard, {
      props: { open: true },
    })
    await flushAsync()
    await advanceToReview(getByText)

    const launchBtn = Array.from(
      document.querySelectorAll("button"),
    ).find((b) => b.textContent?.trim() === "Launch") as HTMLButtonElement
    await fireEvent.click(launchBtn)
    await flushAsync()

    progressCallback?.({
      commandId: "cmd-1",
      message: "Exit code: 1",
      done: true,
    } as CommandProgress)
    await flushAsync()

    expect(queryByText("Retry")).not.toBeNull()
    unmount()
  })

  it("close-during-launch shows the confirm dialog instead of closing", async () => {
    providers.set([makeProvider("docker")])
    // Hold workspaceUp open so launchRunning stays true
    let resolveUp: ((v: string) => void) | undefined
    workspaceUp.mockImplementation(
      () =>
        new Promise<string>((r) => {
          resolveUp = r
        }),
    )

    const { getByText, queryByText, unmount } = render(WorkspaceWizard, {
      props: { open: true },
    })
    await flushAsync()
    await advanceToReview(getByText)

    const launchBtn = Array.from(
      document.querySelectorAll("button"),
    ).find((b) => b.textContent?.trim() === "Launch") as HTMLButtonElement
    await fireEvent.click(launchBtn)
    await flushAsync()

    // While launchRunning is true (workspaceUp still pending), pressing Escape
    // on the Dialog should surface the cancel confirmation.
    await fireEvent.keyDown(document.body, { key: "Escape", code: "Escape" })
    await flushAsync()

    expect(queryByText(/Cancel workspace creation/i)).not.toBeNull()

    // Tidy up: resolve the pending promise so the component finishes.
    resolveUp?.("cmd-1")
    await flushAsync()
    unmount()
  })
})

describe("launch watchdog", () => {
  beforeEach(() => {
    workspaceUp.mockReset()
    onCommandProgress.mockReset()
    providers.set([])
    workspaces.set([])
    progressCallback = null

    onCommandProgress.mockImplementation(
      async (cb: (progress: CommandProgress) => void) => {
        progressCallback = cb
        return () => {
          progressCallback = null
        }
      },
    )

    workspaceUp.mockImplementation(async () => "cmd-1")

    vi.useFakeTimers({ shouldAdvanceTime: true })
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.clearAllMocks()
    document.body.innerHTML = ""
  })

  it("fires after 10 minutes when no done event arrives", async () => {
    providers.set([makeProvider("docker")])
    const { getByText, queryByText, unmount } = render(WorkspaceWizard, {
      props: { open: true },
    })
    await flushAsync()
    await advanceToReview(getByText)

    const launchBtn = Array.from(
      document.querySelectorAll("button"),
    ).find((b) => b.textContent?.trim() === "Launch") as HTMLButtonElement
    await fireEvent.click(launchBtn)
    await flushAsync()

    // Advance past the 10-minute watchdog.
    await vi.advanceTimersByTimeAsync(10 * 60 * 1000 + 1)
    await flushAsync()

    expect(queryByText(/timed out after 10 minutes/i)).not.toBeNull()
    expect(queryByText("Open Workspace")).toBeNull()
    expect(queryByText("Retry")).not.toBeNull()
    unmount()
  })

  it("late done event after watchdog firing does not overwrite the error", async () => {
    providers.set([makeProvider("docker")])
    const { getByText, queryByText, unmount } = render(WorkspaceWizard, {
      props: { open: true },
    })
    await flushAsync()
    await advanceToReview(getByText)

    const launchBtn = Array.from(
      document.querySelectorAll("button"),
    ).find((b) => b.textContent?.trim() === "Launch") as HTMLButtonElement
    await fireEvent.click(launchBtn)
    await flushAsync()

    // Trip the watchdog.
    await vi.advanceTimersByTimeAsync(10 * 60 * 1000 + 1)
    await flushAsync()

    expect(queryByText(/timed out after 10 minutes/i)).not.toBeNull()

    // Late done event (the listener should have been disposed, so this is a no-op).
    progressCallback?.({
      commandId: "cmd-1",
      message: "Exit code: 0",
      done: true,
    } as CommandProgress)
    await flushAsync()

    // Timeout error is still visible; Open Workspace did not appear.
    expect(queryByText(/timed out after 10 minutes/i)).not.toBeNull()
    expect(queryByText("Open Workspace")).toBeNull()
    unmount()
  })
})
