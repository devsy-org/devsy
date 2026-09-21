import { render, screen } from "@testing-library/svelte"
import { afterEach, describe, expect, it, vi } from "vitest"

vi.mock("$lib/components/update/UpdatesPanel.svelte", () => ({
  default: vi.fn(),
}))

import SettingsPage from "./SettingsPage.svelte"

describe("SettingsPage layout", () => {
  afterEach(() => {
    document.body.innerHTML = ""
  })

  it("renders all settings sections as one page without section navigation links", () => {
    render(SettingsPage)

    expect(screen.queryByRole("navigation", { name: "Settings sections" })).toBeNull()
    expect(document.querySelectorAll('a[href^="#"]')).toHaveLength(0)

    for (const name of ["General", "Appearance", "Updates", "Advanced"]) {
      expect(screen.getByRole("heading", { name, level: 2 })).toBeTruthy()
    }
  })
})
