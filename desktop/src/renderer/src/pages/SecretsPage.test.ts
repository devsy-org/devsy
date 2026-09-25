import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/svelte"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

const mocks = vi.hoisted(() => ({
  secretAttach: vi.fn().mockResolvedValue(undefined),
  secretDetach: vi.fn().mockResolvedValue(undefined),
  secretDelete: vi.fn().mockResolvedValue(undefined),
  refreshSecrets: vi.fn().mockResolvedValue(undefined),
}))
vi.mock("$lib/ipc/commands.js", () => ({
  secretAttach: mocks.secretAttach,
  secretDelete: mocks.secretDelete,
  secretDetach: mocks.secretDetach,
  secretSet: vi.fn(),
}))
vi.mock("$lib/stores/secrets.js", async () => {
  const { writable } = await import("svelte/store")
  return {
    secretsLoading: writable(false),
    secretsError: writable(null),
    secrets: writable([
      { name: "ATTACHED", context: "default", attached: true },
      { name: "DETACHED", context: "default", attached: false },
      { name: "STAGING_ONLY", context: "staging", attached: false },
    ]),
    refreshSecrets: mocks.refreshSecrets,
  }
})

import SecretsPage from "./SecretsPage.svelte"

describe("SecretsPage managed secret attachments", () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })
  afterEach(() => {
    cleanup()
  })

  it("renders attachment state and sends attach/detach intent", async () => {
    render(SecretsPage)

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
      expect(mocks.secretAttach).toHaveBeenCalledWith("DETACHED", "default"),
    )
    expect(mocks.refreshSecrets).toHaveBeenCalled()

    await fireEvent.click(
      screen.getByRole("switch", { name: "Inject ATTACHED into workspaces" }),
    )
    await waitFor(() =>
      expect(mocks.secretDetach).toHaveBeenCalledWith("ATTACHED", "default"),
    )
  })

  it("uses the context displayed on a stale row", async () => {
    render(SecretsPage)

    await fireEvent.click(
      screen.getByRole("switch", {
        name: "Inject STAGING_ONLY into workspaces",
      }),
    )
    await waitFor(() =>
      expect(mocks.secretAttach).toHaveBeenCalledWith(
        "STAGING_ONLY",
        "staging",
      ),
    )
  })
})
