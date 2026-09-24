import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

const mocks = vi.hoisted(() => ({
  envAttach: vi.fn().mockResolvedValue(undefined),
  envDetach: vi.fn().mockResolvedValue(undefined),
  refreshEnv: vi.fn().mockResolvedValue(undefined),
}))
vi.mock("$lib/ipc/commands.js", () => ({
  envAttach: mocks.envAttach,
  envDelete: vi.fn(),
  envDetach: mocks.envDetach,
  envSet: vi.fn(),
}))
vi.mock("$lib/stores/env.js", async () => {
  const { writable } = await import("svelte/store")
  return {
    envLoading: writable(false),
    envVars: writable([
      { name: "ATTACHED", value: "one", context: "default", attached: true },
      { name: "DETACHED", value: "two", context: "default", attached: false },
      { name: "STAGING_ONLY", value: "three", context: "staging", attached: false },
    ]),
    refreshEnv: mocks.refreshEnv,
  }
})

import EnvPage from "./EnvPage.svelte"

describe("EnvPage managed environment attachments", () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })
  afterEach(() => {
    cleanup()
  })

  it("renders attachment state and sends attach/detach intent", async () => {
    render(EnvPage)

    expect(
      screen
        .getByRole("switch", { name: "Inject ATTACHED into workspaces" })
        .getAttribute("aria-checked"),
    ).toBe("true")
    const detached = screen.getByRole("switch", {
      name: "Inject DETACHED into workspaces",
    })
    expect(detached.getAttribute("aria-checked")).toBe("false")

    await fireEvent.click(detached)
    await waitFor(() =>
      expect(mocks.envAttach).toHaveBeenCalledWith("DETACHED", "default"),
    )
    expect(mocks.refreshEnv).toHaveBeenCalled()

    await fireEvent.click(
      screen.getByRole("switch", { name: "Inject ATTACHED into workspaces" }),
    )
    await waitFor(() =>
      expect(mocks.envDetach).toHaveBeenCalledWith("ATTACHED", "default"),
    )
  })

  it("uses the context displayed on a stale row", async () => {
    render(EnvPage)

    await fireEvent.click(
      screen.getByRole("switch", { name: "Inject STAGING_ONLY into workspaces" }),
    )
    await waitFor(() =>
      expect(mocks.envAttach).toHaveBeenCalledWith("STAGING_ONLY", "staging"),
    )
  })
})
