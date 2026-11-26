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
      {
        name: "LOCKED_KEY",
        context: "default",
        attached: true,
        availability: "locked",
      },
      {
        name: "MISSING_KEY",
        context: "default",
        attached: false,
        availability: "missing",
      },
      {
        name: "BACKEND_KEY",
        context: "default",
        attached: false,
        availability: "backend_unavailable",
      },
    ]),
    refreshSecrets: mocks.refreshSecrets,
  }
})

import SecretsPage from "$lib/components/variables/SecretsVariablesPanel.svelte"

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

  it("shows availability states while keeping locked secret metadata usable", () => {
    render(SecretsPage)

    expect(screen.getByRole("table")).toBeTruthy()
    expect(screen.getByText("Locked")).toBeTruthy()
    expect(screen.getByText("Missing value")).toBeTruthy()
    expect(screen.getByText("Unavailable")).toBeTruthy()
    expect(
      screen.getByRole("switch", { name: "Inject LOCKED_KEY into workspaces" }),
    ).toBeTruthy()
  })
  it("sorts rows by name and searches context", async () => {
    render(SecretsPage)
    const names = Array.from(
      screen.getByRole("table").querySelectorAll("tbody tr"),
    ).map((row) => row.querySelector("button")?.textContent?.trim())
    expect(names).toEqual([
      "ATTACHED",
      "BACKEND_KEY",
      "DETACHED",
      "LOCKED_KEY",
      "MISSING_KEY",
      "STAGING_ONLY",
    ])
    await fireEvent.input(
      screen.getByRole("textbox", { name: "Search secrets" }),
      { target: { value: "staging" } },
    )
    expect(screen.getByRole("button", { name: "STAGING_ONLY" })).toBeTruthy()
    expect(screen.queryByRole("button", { name: "ATTACHED" })).toBeNull()
  })

  it("restores the switch after an attachment failure", async () => {
    mocks.secretAttach.mockRejectedValueOnce(new Error("Failed request"))
    render(SecretsPage)
    await fireEvent.click(
      screen.getByRole("switch", { name: "Inject DETACHED into workspaces" }),
    )
    await waitFor(() =>
      expect(screen.getByRole("alert").textContent).toContain(
        "Unable to update workspace injection. Try again.",
      ),
    )
    expect(
      screen
        .getByRole("switch", { name: "Inject DETACHED into workspaces" })
        .getAttribute("aria-checked"),
    ).toBe("false")
    expect(screen.queryByText("Secret details")).toBeNull()
  })

  it("deletes the exact row context after explicit confirmation", async () => {
    render(SecretsPage)
    const actions = screen.getByRole("button", {
      name: "Actions for STAGING_ONLY",
    })
    await fireEvent.click(actions)
    const menuItem = await screen.findByRole("menuitem", {
      name: "Delete",
    })
    await fireEvent.click(menuItem)
    await fireEvent.click(screen.getByRole("button", { name: "Delete" }))
    await waitFor(() =>
      expect(mocks.secretDelete).toHaveBeenCalledWith(
        "STAGING_ONLY",
        "staging",
      ),
    )
  })
})
