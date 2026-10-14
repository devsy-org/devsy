import { cleanup, fireEvent, render, screen } from "@testing-library/svelte"
import { get, writable } from "svelte/store"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

const { refreshProtection, replace } = vi.hoisted(() => ({
  refreshProtection: vi.fn(async () => {}),
  replace: vi.fn(async (_path: string) => {}),
}))
vi.mock("$lib/router.js", () => ({ querystring: writable(""), replace }))
vi.mock("$lib/stores/secret-protection.js", () => ({
  initSecretProtection: refreshProtection,
}))
vi.mock("$lib/components/variables/SecretsVariablesPanel.svelte", () => ({
  default: vi.fn(),
}))
vi.mock("$lib/components/variables/EnvironmentVariablesPanel.svelte", () => ({
  default: vi.fn(),
}))
vi.mock("$lib/components/variables/SecretSecurityBanner.svelte", () => ({
  default: vi.fn(),
}))
vi.mock("$lib/components/variables/SecretSecuritySheet.svelte", () => ({
  default: vi.fn(),
}))

import { querystring } from "$lib/router.js"
import VariablesPage from "./VariablesPage.svelte"

const query = querystring as ReturnType<typeof writable<string>>

describe("Workspace Variables navigation", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    query.set("")
    replace.mockImplementation(async (path) =>
      query.set(path.split("?")[1] ?? ""),
    )
  })
  afterEach(cleanup)

  it.each(["", "tab=invalid"])(
    "defaults to Secrets for query %s",
    async (qs) => {
      query.set(qs)
      render(VariablesPage)
      expect(
        screen.getByRole("heading", { name: "Workspace Variables" }),
      ).toBeTruthy()
      expect(screen.getByRole("button", { name: "Add secret" })).toBeTruthy()
      expect(
        screen
          .getByRole("tab", { name: "Secrets" })
          .getAttribute("aria-selected"),
      ).toBe("true")
      await vi.waitFor(() => expect(refreshProtection).toHaveBeenCalledOnce())
    },
  )

  it("honors the environment deep link and shows only its primary action", () => {
    query.set("tab=env")
    render(VariablesPage)
    expect(screen.getByRole("button", { name: "Add variable" })).toBeTruthy()
    expect(screen.queryByRole("button", { name: "Add secret" })).toBeNull()
    expect(
      screen
        .getByRole("tab", { name: "Environment Variables" })
        .getAttribute("aria-selected"),
    ).toBe("true")
    expect(refreshProtection).not.toHaveBeenCalled()
  })

  it("updates the URL on tab selection and responds to history query changes", async () => {
    render(VariablesPage)
    await fireEvent.click(
      screen.getByRole("tab", { name: "Environment Variables" }),
    )
    await vi.waitFor(() =>
      expect(replace).toHaveBeenCalledWith("/variables?tab=env"),
    )
    expect(get(query)).toBe("tab=env")
    await vi.waitFor(() =>
      expect(screen.getByRole("button", { name: "Add variable" })).toBeTruthy(),
    )
    query.set("tab=secrets")
    await vi.waitFor(() =>
      expect(screen.getByRole("button", { name: "Add secret" })).toBeTruthy(),
    )
  })
})
